package karpenter

import (
	"context"
	"fmt"
	"log"
	"strings"

	tfv1 "github.com/NexusGPU/tensor-fusion/api/v1"
	"github.com/NexusGPU/tensor-fusion/internal/cloudprovider/types"
	"github.com/mitchellh/mapstructure"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// KarpenterConfig holds Karpenter-specific configuration parsed from ExtraParams
type KarpenterConfig struct {
	// NodeClass configuration
	NodeClass struct {
		Group     string `mapstructure:"group"`
		Kind      string `mapstructure:"kind"`
		Name      string `mapstructure:"name"`
		Namespace string `mapstructure:"namespace"`
	} `mapstructure:"nodeclass"`

	// NodeClaim configuration
	NodeClaim struct {
		Namespace string `mapstructure:"namespace"`
	} `mapstructure:"nodeclaim"`
	// Additional Karpenter settings
	CustomLabels map[string]string `mapstructure:"labels"`

	// GPU resource name, default to "nvidia.com/gpu"
	GPUResourceName corev1.ResourceName `mapstructure:"gpuresource"`
}

type KarpenterGPUNodeProvider struct {
	client client.Client
	config tfv1.ComputingVendorConfig
}

func NewKarpenterGPUNodeProvider(cfg tfv1.ComputingVendorConfig, client client.Client) (KarpenterGPUNodeProvider, error) {
	// Validate configuration
	if cfg.Type != tfv1.ComputingVendorKarpenter {
		return KarpenterGPUNodeProvider{}, fmt.Errorf("invalid computing vendor type: expected 'karpenter', got '%s'", cfg.Type)
	}
	if client == nil {
		return KarpenterGPUNodeProvider{}, fmt.Errorf("kubernetes client cannot be nil")
	}
	// Initialize the Karpenter GPU Node Provider with the provided client
	return KarpenterGPUNodeProvider{
		client: client,
		config: cfg,
	}, nil
}

func (p KarpenterGPUNodeProvider) TestConnection() error {
	if p.client == nil {
		return fmt.Errorf("kubernetes client is not initialized")
	}

	if err := p.client.List(context.Background(), &karpenterv1.NodePoolList{}); err != nil {
		return fmt.Errorf("karpenter NodePool CRD not found or not accessible: %w", err)
	}

	fmt.Println("Testing Karpenter connection - checking client availability")
	return nil
}

func (p KarpenterGPUNodeProvider) CreateNode(ctx context.Context, param *types.NodeCreationParam) (*types.GPUNodeStatus, error) {
	if param == nil || p.client == nil {
		return nil, fmt.Errorf("NodeCreationParam cannot be nil")
	}
	// Build NodeClaim from the creation parameters
	// NodeName is the UUID
	nodeClaim := p.buildNodeClaim(param, param.NodeName)
	if nodeClaim == nil {
		return nil, fmt.Errorf("failed to build NodeClaim for node %s", param.NodeName)
	}
	// Create the NodeClaim using the Karpenter client
	err := p.client.Create(ctx, nodeClaim)
	if err != nil {
		return nil, fmt.Errorf("failed to create NodeClaim %s: %v", nodeClaim.Name, err)
	}

	// Log the NodeClaim creation
	fmt.Printf("Created NodeClaim: %s with instance type: %s in region: %s\n",
		nodeClaim.Name, param.InstanceType, param.Region)

	// Return the initial status - actual node creation will be handled by Karpenter
	status := &types.GPUNodeStatus{
		InstanceID: param.NodeName, // Use nodename as instance ID
		CreatedAt:  metav1.Now().Time,
		PrivateIP:  "", // Will be populated once node is actually provisioned
		PublicIP:   "", // Will be populated once node is actually provisioned
	}
	return status, nil
}

func (p KarpenterGPUNodeProvider) TerminateNode(ctx context.Context, param *types.NodeIdentityParam) error {
	if param == nil {
		return fmt.Errorf("NodeIdentityParam cannot be nil")
	}

	if p.client == nil {
		return fmt.Errorf("kubernetes client is not initialized")
	}

	// Delete the NodeClaim, Karpenter will handle the node termination
	nodeClaim := &karpenterv1.NodeClaim{}
	err := p.client.Get(ctx, client.ObjectKey{
		Name: param.InstanceID, // Using instance ID as NodeClaim name
	}, nodeClaim)
	if err != nil {
		return fmt.Errorf("failed to find NodeClaim for instance %s: %v", param.InstanceID, err)
	}
	err = p.client.Delete(ctx, nodeClaim)
	if err != nil {
		return fmt.Errorf("failed to delete NodeClaim for instance %s: %v", param.InstanceID, err)
	}
	fmt.Printf("Terminated NodeClaim for instance: %s in region: %s\n",
		param.InstanceID, param.Region)

	return nil
}

func (p KarpenterGPUNodeProvider) GetNodeStatus(ctx context.Context, param *types.NodeIdentityParam) (*types.GPUNodeStatus, error) {
	if param == nil {
		return nil, fmt.Errorf("NodeIdentityParam cannot be nil")
	}

	if p.client == nil {
		return nil, fmt.Errorf("kubernetes client is not initialized")
	}

	// Get the NodeClaim status
	nodeClaim := &karpenterv1.NodeClaim{}
	err := p.client.Get(ctx, client.ObjectKey{
		Name: param.InstanceID,
	}, nodeClaim)
	if err != nil {
		return nil, fmt.Errorf("failed to get NodeClaim for instance %s: %v", param.InstanceID, err)
	}

	// Convert NodeClaim status to GPUNodeStatus
	status := &types.GPUNodeStatus{
		InstanceID: param.InstanceID,
		CreatedAt:  nodeClaim.CreationTimestamp.Time,
		PrivateIP:  "",
		PublicIP:   "",
	}

	k8sNode := &corev1.Node{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: param.InstanceID}, k8sNode); err == nil {
		// Node exists, fill in IP, status, and architecture
		for _, a := range k8sNode.Status.Addresses {
			switch a.Type {
			case corev1.NodeInternalIP:
				status.PrivateIP = a.Address
			case corev1.NodeExternalIP:
				status.PublicIP = a.Address
			}
		}
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get Node %q failed: %w", param.InstanceID, err)
	} else {
		// Node not register
		log.Printf("Node %q not registered in cluster", param.InstanceID)
	}
	return status, nil
}

func (p KarpenterGPUNodeProvider) GetInstancePricing(instanceType string, region string, capacityType types.CapacityTypeEnum) (float64, error) {
	// TODO how to do it
	return 0.0, fmt.Errorf("pricing not implemented for Karpenter provider - delegate to underlying cloud provider")
}

func (p KarpenterGPUNodeProvider) GetGPUNodeInstanceTypeInfo(region string) []types.GPUNodeInstanceInfo {
	return []types.GPUNodeInstanceInfo{}
}

// parseKarpenterConfig extracts Karpenter-specific configuration from ExtraParams
func (p KarpenterGPUNodeProvider) parseKarpenterConfig(param *types.NodeCreationParam) *KarpenterConfig {
	karpenterConfig := &KarpenterConfig{}
	extraParams := param.ExtraParams
	if extraParams == nil {
		return karpenterConfig
	}

	// map -> structure
	karpenterData := make(map[string]interface{})

	for key, value := range extraParams {
		if !strings.HasPrefix(key, "karpenter.") {
			continue
		}

		// remove "karpenter." prefix
		karpenterKey := strings.TrimPrefix(key, "karpenter.")

		// build nested map structure
		setNestedValue(karpenterData, karpenterKey, value)
	}

	// decode
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:      karpenterConfig,
		TagName:     "mapstructure",
		ErrorUnused: false, // allow unused fields
	})
	if err != nil {
		// if decoder creation failed, return empty config
		return &KarpenterConfig{}
	}

	if err := decoder.Decode(karpenterData); err != nil {
		// if decode failed, return empty config
		return &KarpenterConfig{}
	}

	// set default values
	if karpenterConfig.NodeClass.Group == "" {
		karpenterConfig.NodeClass.Group = "karpenter.k8s.aws"
	}
	if karpenterConfig.NodeClass.Kind == "" {
		karpenterConfig.NodeClass.Kind = "EC2NodeClass"
	}
	if karpenterConfig.NodeClaim.Namespace == "" {
		karpenterConfig.NodeClaim.Namespace = "karpenter"
	}
	if karpenterConfig.GPUResourceName == "" {
		karpenterConfig.GPUResourceName = "nvidia.com/gpu"
	}

	return karpenterConfig
}

// setNestedValue set value in nested map, support dot-separated path
func setNestedValue(data map[string]interface{}, path string, value string) {
	parts := strings.Split(path, ".")
	current := data

	// traverse to the second last layer
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		if _, exists := current[part]; !exists {
			current[part] = make(map[string]interface{})
		}
		if nextMap, ok := current[part].(map[string]interface{}); ok {
			current = nextMap
		} else {
			// if not map, cannot continue nested
			return
		}
	}

	// set final value
	current[parts[len(parts)-1]] = value
}

func (p KarpenterGPUNodeProvider) buildNodeClaim(param *types.NodeCreationParam, instanceId string) *karpenterv1.NodeClaim {
	if param == nil {
		return nil
	}

	// Parse Karpenter configuration from ExtraParams
	karpenterConfig := p.parseKarpenterConfig(param)

	// Build resource requirements - only include resources that Karpenter understands
	resourceRequests := corev1.ResourceList{}

	// Add GPU resources if specified (Karpenter supports nvidia.com/gpu)
	if param.GPUDeviceOffered > 0 {
		resourceRequests[karpenterConfig.GPUResourceName] = resource.MustParse(fmt.Sprintf("%d", param.GPUDeviceOffered))
	}

	// These requirements are handled through:
	// 1. Instance type selection (done in CalculateLeastCostGPUNodes)
	// 2. Node labels (for TensorFusion's resource management)
	// 3. NodeSelector requirements (for architecture/zone matching)

	// Build node selector requirements using Karpenter's NodeSelectorRequirement
	requirements := []karpenterv1.NodeSelectorRequirementWithMinValues{}

	// 1. instance type
	if param.InstanceType != "" {
		requirements = append(requirements, karpenterv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      "node.kubernetes.io/instance-type",
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{param.InstanceType},
			},
		})
	}
	// 2.capacity type
	if param.CapacityType != "" {
		requirements = append(requirements, karpenterv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      "karpenter.sh/capacity-type",
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{strings.ToLower(string(param.CapacityType))},
			},
		})
	}

	// 3. zone
	if param.Zone != "" {
		requirements = append(requirements, karpenterv1.NodeSelectorRequirementWithMinValues{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      "topology.kubernetes.io/zone",
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{param.Zone},
			},
		})
	}

	// Build NodeClass reference if provided
	var nodeClassRef *karpenterv1.NodeClassReference
	var nodeClassName = karpenterConfig.NodeClass.Name

	if nodeClassName != "" {
		// Determine NodeClass Kind and Group based on configuration
		group, kind := karpenterConfig.NodeClass.Group, karpenterConfig.NodeClass.Kind
		nodeClassRef = &karpenterv1.NodeClassReference{
			Group: group,
			Kind:  kind,
			Name:  nodeClassName,
		}
	}

	// Create NodeClaim using Karpenter's official type
	nodeClaim := &karpenterv1.NodeClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "karpenter.sh/v1",
			Kind:       "NodeClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      param.NodeName,
			Namespace: karpenterConfig.NodeClaim.Namespace,
			Annotations: map[string]string{
				// Protection annotation to prevent unexpected deletion by Karpenter
				"karpenter.sh/do-not-disrupt": "true",
			},
			Labels: map[string]string{
				"tensor-fusion.io/managed":       "true",
				"tensor-fusion.io/node-name":     param.NodeName,
				"tensor-fusion.io/instance-type": param.InstanceType,
				"tensor-fusion.io/region":        param.Region,
				"tensor-fusion.io/instance-id":   instanceId,
			},
		},
		Spec: karpenterv1.NodeClaimSpec{
			Requirements: requirements,
			NodeClassRef: nodeClassRef,
			Resources: karpenterv1.ResourceRequirements{
				Requests: resourceRequests,
			},
		},
	}

	// Add AWS-specific requirements if NodeClass is provided
	requirements = p.addAWSSpecificRequirements(param, requirements)

	// Apply custom labels from Karpenter configuration
	for key, value := range karpenterConfig.CustomLabels {
		nodeClaim.Labels[key] = value
	}
	// Update the nodeClaim spec with the final requirements
	nodeClaim.Spec.Requirements = requirements

	return nodeClaim
}

// addAWSSpecificRequirements adds AWS-specific requirements from NodeClass to the requirements slice
func (p KarpenterGPUNodeProvider) addAWSSpecificRequirements(param *types.NodeCreationParam, requirements []karpenterv1.NodeSelectorRequirementWithMinValues) []karpenterv1.NodeSelectorRequirementWithMinValues {
	// Add NodeClass-specific requirements and metadata if provided
	if param.NodeClass != nil {
		// Add subnet requirements from NodeClass - only for AWS
		for _, subnetTerm := range param.NodeClass.Spec.SubnetSelectorTerms {
			if subnetTerm.ID != "" {
				requirements = append(requirements, karpenterv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: corev1.NodeSelectorRequirement{
						Key:      "karpenter.k8s.aws/subnet-id",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{subnetTerm.ID},
					},
				})
			}
		}
		// Add security group requirements from NodeClass - only for AWS
		for _, sgTerm := range param.NodeClass.Spec.SecurityGroupSelectorTerms {
			if sgTerm.ID != "" {
				requirements = append(requirements, karpenterv1.NodeSelectorRequirementWithMinValues{
					NodeSelectorRequirement: corev1.NodeSelectorRequirement{
						Key:      "karpenter.k8s.aws/security-group-id",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{sgTerm.ID},
					},
				})
			}
		}
	}
	return requirements
}
