package controller

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"github.com/NexusGPU/tensor-fusion/internal/cloudprovider/common"
	"github.com/NexusGPU/tensor-fusion/internal/cloudprovider/types"
	"github.com/NexusGPU/tensor-fusion/internal/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	cloudprovider "github.com/NexusGPU/tensor-fusion/internal/cloudprovider"
)

// Controller and trigger logic for abstract layer of node provisioning
func (r *GPUPoolReconciler) reconcilePoolCapacityWithProvisioner(ctx context.Context, pool *tfv1.GPUPool) (map[string]tfv1.Resource, error) {
	if !ProvisioningToggle {
		return nil, nil
	}

	log := log.FromContext(ctx)
	// check if min resource constraint is satisfied
	shouldScaleUp := false
	tflopsGap := int64(0)
	vramGap := int64(0)

	assumedTflops := int64(0)
	assumedVRAM := int64(0)

	for claimName := range PendingGPUNodeClaim[pool.Name] {
		gpuNodeClaim := tfv1.GPUNodeClaim{}
		if err := r.Get(ctx, client.ObjectKey{Name: claimName}, &gpuNodeClaim); err != nil {
			return nil, err
		}
		pendingTflops, _ := gpuNodeClaim.Spec.TFlopsOffered.AsInt64()
		pendingVRAM, _ := gpuNodeClaim.Spec.VRAMOffered.AsInt64()
		assumedTflops += pendingTflops
		assumedVRAM += pendingVRAM
	}

	totalTFlops, _ := pool.Status.TotalTFlops.AsInt64()
	totalVRAM, _ := pool.Status.TotalVRAM.AsInt64()
	totalTFlops += assumedTflops
	totalVRAM += assumedVRAM

	// default warmUp is zero, only scale up when available < 0
	warmUpTFlops := int64(0)
	warmUpVRAM := int64(0)
	if pool.Spec.CapacityConfig.WarmResources != nil {
		warmUpTFlops, _ = pool.Spec.CapacityConfig.WarmResources.TFlops.AsInt64()
		warmUpVRAM, _ = pool.Spec.CapacityConfig.WarmResources.VRAM.AsInt64()
	}

	if pool.Spec.CapacityConfig.MinResources != nil {
		minTFlops, _ := pool.Spec.CapacityConfig.MinResources.TFlops.AsInt64()
		minVRAM, _ := pool.Spec.CapacityConfig.MinResources.VRAM.AsInt64()

		tflopsGap = minTFlops - totalTFlops
		vramGap = minVRAM - totalVRAM

		shouldScaleUp = (tflopsGap > 0) || (vramGap > 0)
		if shouldScaleUp {
			log.Info("Should scale up GPU node due gap of currentTotal <-> min capacity", "pool", pool.Name)
		}
	}

	// Only check warm-up when everything is ready, otherwise it will cause duplicated resource creation
	if !shouldScaleUp && pool.Status.Phase == tfv1.TensorFusionPoolPhaseRunning {
		availableTFlops, _ := pool.Status.AvailableTFlops.AsInt64()
		availableVRAM, _ := pool.Status.AvailableVRAM.AsInt64()
		availableTFlops += assumedTflops
		availableVRAM += assumedVRAM

		tflopsGap = warmUpTFlops - availableTFlops
		vramGap = warmUpVRAM - availableVRAM

		shouldScaleUp = (tflopsGap > 0) || (vramGap > 0)
		if shouldScaleUp {
			log.Info("Should scale up GPU node due gap of available <-> warmup capacity", "pool", pool.Name)
		}
	}

	if shouldScaleUp && pool.Spec.CapacityConfig.MaxResources != nil {
		maxTFlops, _ := pool.Spec.CapacityConfig.MaxResources.TFlops.AsInt64()
		maxVRAM, _ := pool.Spec.CapacityConfig.MaxResources.VRAM.AsInt64()

		if totalTFlops >= maxTFlops || totalVRAM >= maxVRAM {
			shouldScaleUp = false

			log.Info("Should NOT scale up GPU node due to max capacity constraint", "pool", pool.Name)

			r.Recorder.Eventf(pool, corev1.EventTypeWarning, "MaxResourceConstraintReached", "Max resource constraint can not be satisfied, can not scale up: %v", pool.Spec.CapacityConfig.MaxResources)
		}
	}

	if !shouldScaleUp {
		return nil, nil
	}

	// create provisioner
	provider, cluster, err := createProvisionerAndQueryCluster(ctx, pool, r.Client)
	if err != nil {
		return nil, err
	}

	var nodeClassRef tfv1.GroupKindName
	switch pool.Spec.NodeManagerConfig.ProvisioningMode {
	case tfv1.ProvisioningModeProvisioned:
		if pool.Spec.NodeManagerConfig.NodeProvisioner == nil {
			return nil, fmt.Errorf("failed to get node provisioner config for pool %s", pool.Name)
		}
		if pool.Spec.NodeManagerConfig.NodeProvisioner.NodeClass == "" {
			return nil, fmt.Errorf("failed to get node class for pool %s", pool.Name)
		}
		nodeClassRef = tfv1.GroupKindName{
			Name:    pool.Spec.NodeManagerConfig.NodeProvisioner.NodeClass,
			Kind:    tfv1.GPUNodeClassKind,
			Group:   tfv1.GroupVersion.Group,
			Version: tfv1.GroupVersion.Version,
		}
	case tfv1.ProvisioningModeKarpenter:
		if pool.Spec.NodeManagerConfig.NodeProvisioner.KarpenterNodeClassRef == nil {
			return nil, fmt.Errorf("failed to get karpenter node class ref for pool %s", pool.Name)
		}
		nodeClassRef = *pool.Spec.NodeManagerConfig.NodeProvisioner.KarpenterNodeClassRef
	}

	// convert resource gap to least cost GPUNode creation param
	gpuNodeParams, err := common.CalculateLeastCostGPUNodes(ctx, provider, cluster, pool, nodeClassRef, tflopsGap, vramGap)
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	wg.Add(len(gpuNodeParams))

	var errList []error

	// lock the pool before next node scaling up loop, add assumed scaling resources util all pending nodeClaims are running
	newCreatedNodes := map[string]tfv1.Resource{}
	for _, nodeClaim := range gpuNodeParams {
		go func(nodeClaim tfv1.GPUNodeClaimSpec) {
			defer wg.Done()

			// Create GPUNode custom resource immediately and GPUNode controller will watch the K8S node to be ready
			// Persist the status to GPUNode to avoid duplicated creation in next reconciliation
			// If the K8S node never be ready after some time, the GPUNode will be deleted, then the Pool reconcile loop can scale up and meet the capacity constraint again

			costPerHour, pricingErr := provider.GetInstancePricing(nodeClaim.InstanceType, nodeClaim.CapacityType, nodeClaim.Region)
			if pricingErr != nil {
				errList = append(errList, pricingErr)
				return
			}

			gpuNodeClaimRes := &tfv1.GPUNodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeClaim.NodeName,
					Labels: map[string]string{
						constants.LabelKeyOwner:        pool.Name,
						constants.LabelKeyClusterOwner: cluster.Name,
						constants.LabelKeyNodeClass:    nodeClassRef.Name,

						// to be compatible with nodeSelector mode, allow GPUNode controller to start HyperVisor pod
						fmt.Sprintf(constants.GPUNodePoolIdentifierLabelFormat, pool.Name): "true",
					},
					Annotations: map[string]string{
						constants.PricingAnnotation: strconv.FormatFloat(costPerHour, 'f', 6, 64),
					},
				},
				Spec: nodeClaim,
			}
			_ = controllerutil.SetControllerReference(pool, gpuNodeClaimRes, r.Scheme)
			err := r.Create(ctx, gpuNodeClaimRes)
			if err != nil {
				errList = append(errList, err)
				return
			}
			log.Info("Created new GPUNode claim", "gpuNodeClaimName", nodeClaim.NodeName)
			newCreatedNodes[nodeClaim.NodeName] = tfv1.Resource{
				Tflops: nodeClaim.TFlopsOffered,
				Vram:   nodeClaim.VRAMOffered,
			}
		}(nodeClaim)
	}

	wg.Wait()

	if len(errList) > 0 {
		return nil, fmt.Errorf("failed to create nodes: %v", errList)
	}
	return newCreatedNodes, nil
}

func createProvisionerAndQueryCluster(ctx context.Context, pool *tfv1.GPUPool, r client.Client) (types.GPUNodeProvider, *tfv1.TensorFusionCluster, error) {

	if pool.Spec.NodeManagerConfig == nil || pool.Spec.NodeManagerConfig.NodeProvisioner == nil {
		return nil, nil, fmt.Errorf("failed to get node provisioner config for pool %s", pool.Name)
	}

	clusterName := pool.Labels[constants.LabelKeyOwner]
	if clusterName == "" {
		return nil, nil, fmt.Errorf("failed to get cluster name for pool %s", pool.Name)
	}

	cluster := tfv1.TensorFusionCluster{}
	if err := r.Get(ctx, client.ObjectKey{Name: clusterName}, &cluster); err != nil {
		return nil, nil, err
	}

	vendorCfg := cluster.Spec.ComputingVendor
	if vendorCfg == nil {
		return nil, nil, fmt.Errorf("failed to get computing vendor config for cluster %s", clusterName)
	}

	provider, err := cloudprovider.GetProvider(ctx, *vendorCfg, r, pool.Spec.NodeManagerConfig)
	if err != nil {
		return nil, nil, err
	}

	return provider, &cluster, nil
}
