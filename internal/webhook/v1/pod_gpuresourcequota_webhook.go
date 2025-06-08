/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"context"
	"fmt"
	"net/http"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	tensorfusionv1 "github.com/NexusGPU/tensor-fusion/api/v1"
)

// GPUResourceQuotaValidator validates TensorFusionWorkload against GPUResourceQuota
type GPUResourceQuotaValidator struct {
	Client  client.Client
	decoder admission.Decoder
}

// +kubebuilder:webhook:path=/validate-tensor-fusion-ai-v1-tensorfusionworkload,mutating=false,failurePolicy=fail,sideEffects=None,groups=tensor-fusion.ai,resources=tensorfusionworkloads,verbs=create;update,versions=v1,name=vgpuresourcequota.kb.io,admissionReviewVersions=v1

// Handle validates TensorFusionWorkload against GPUResourceQuota
func (v *GPUResourceQuotaValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := log.FromContext(ctx)

	workload := &tensorfusionv1.TensorFusionWorkload{}
	if err := v.decoder.Decode(req, workload); err != nil {
		logger.Error(err, "Failed to decode TensorFusionWorkload")
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Basic spec validation
	if err := v.validateWorkloadSpec(workload); err != nil {
		logger.Info("Workload spec validation failed", "error", err)
		return admission.Denied(err.Error())
	}

	// Get GPUResourceQuota (if exists)
	quota := &tensorfusionv1.GPUResourceQuota{}
	quotaKey := types.NamespacedName{
		Name:      workload.Namespace,
		Namespace: workload.Namespace,
	}

	quotaExists := true
	if err := v.Client.Get(ctx, quotaKey, quota); err != nil {
		if errors.IsNotFound(err) {
			// No quota definition found, skip quota-related validation
			quotaExists = false
			logger.Info("No GPUResourceQuota found for namespace, skipping quota validation", "namespace", workload.Namespace)
		} else {
			logger.Error(err, "Failed to get GPUResourceQuota")
			return admission.Errored(http.StatusInternalServerError, err)
		}
	}

	if quotaExists {
		// Apply default values
		v.applyDefaults(workload, quota)

		// Validate single workload limits
		if err := v.validateSingleWorkloadLimits(workload, quota); err != nil {
			logger.Info("Workload violates single workload limits", "error", err)
			return admission.Denied(err.Error())
		}
	}

	// Business logic validation
	if err := v.validateBusinessLogic(workload); err != nil {
		logger.Info("Workload business logic validation failed", "error", err)
		return admission.Denied(err.Error())
	}

	// Note: Total quota validation is now performed in GPUAllocator.Alloc()
	// If GPUAllocator allocation fails, TensorFusionWorkloadController will handle the failure

	logger.Info("Workload validation passed", "workload", workload.Name, "namespace", workload.Namespace)
	return admission.Allowed("Basic validation passed, quota will be checked during allocation")
}

// applyDefaults applies default values from quota to workload if not specified
func (v *GPUResourceQuotaValidator) applyDefaults(workload *tensorfusionv1.TensorFusionWorkload, quota *tensorfusionv1.GPUResourceQuota) {
	// Apply default TFlops if not specified
	if workload.Spec.Resources.Requests.Tflops.IsZero() && quota.Spec.Single.DefaultRequest != nil && quota.Spec.Single.DefaultRequest.TFlops != nil {
		workload.Spec.Resources.Requests.Tflops = *quota.Spec.Single.DefaultRequest.TFlops
	}

	// Apply default VRAM if not specified
	if workload.Spec.Resources.Requests.Vram.IsZero() && quota.Spec.Single.DefaultRequest != nil && quota.Spec.Single.DefaultRequest.VRAM != nil {
		workload.Spec.Resources.Requests.Vram = *quota.Spec.Single.DefaultRequest.VRAM
	}

	// Apply default limits if not specified
	if workload.Spec.Resources.Limits.Tflops.IsZero() && quota.Spec.Single.Default != nil && quota.Spec.Single.Default.TFlops != nil {
		workload.Spec.Resources.Limits.Tflops = *quota.Spec.Single.Default.TFlops
	}

	if workload.Spec.Resources.Limits.Vram.IsZero() && quota.Spec.Single.Default != nil && quota.Spec.Single.Default.VRAM != nil {
		workload.Spec.Resources.Limits.Vram = *quota.Spec.Single.Default.VRAM
	}
}

// validateSingleWorkloadLimits validates workload against per-workload limits
func (v *GPUResourceQuotaValidator) validateSingleWorkloadLimits(workload *tensorfusionv1.TensorFusionWorkload, quota *tensorfusionv1.GPUResourceQuota) error {
	if quota.Spec.Single.Max == nil {
		return nil
	}

	replicas := int32(1)
	if workload.Spec.Replicas != nil {
		replicas = *workload.Spec.Replicas
	}

	// Check max TFlops
	if quota.Spec.Single.Max.TFlops != nil {
		totalTFlops := workload.Spec.Resources.Requests.Tflops.DeepCopy()
		totalTFlops.Set(totalTFlops.Value() * int64(replicas))
		if totalTFlops.Cmp(*quota.Spec.Single.Max.TFlops) > 0 {
			return fmt.Errorf("workload TFlops (%s) exceeds maximum allowed (%s)", totalTFlops.String(), quota.Spec.Single.Max.TFlops.String())
		}
	}

	// Check max VRAM
	if quota.Spec.Single.Max.VRAM != nil {
		totalVRAM := workload.Spec.Resources.Requests.Vram.DeepCopy()
		totalVRAM.Set(totalVRAM.Value() * int64(replicas))
		if totalVRAM.Cmp(*quota.Spec.Single.Max.VRAM) > 0 {
			return fmt.Errorf("workload VRAM (%s) exceeds maximum allowed (%s)", totalVRAM.String(), quota.Spec.Single.Max.VRAM.String())
		}
	}

	// Check max workers
	if quota.Spec.Single.Max.Workers != nil && replicas > *quota.Spec.Single.Max.Workers {
		return fmt.Errorf("workload replicas (%d) exceeds maximum allowed (%d)", replicas, *quota.Spec.Single.Max.Workers)
	}

	// Check min TFlops
	if quota.Spec.Single.Min != nil && quota.Spec.Single.Min.TFlops != nil {
		if workload.Spec.Resources.Requests.Tflops.Cmp(*quota.Spec.Single.Min.TFlops) < 0 {
			return fmt.Errorf("workload TFlops (%s) is below minimum required (%s)", workload.Spec.Resources.Requests.Tflops.String(), quota.Spec.Single.Min.TFlops.String())
		}
	}

	// Check min VRAM
	if quota.Spec.Single.Min != nil && quota.Spec.Single.Min.VRAM != nil {
		if workload.Spec.Resources.Requests.Vram.Cmp(*quota.Spec.Single.Min.VRAM) < 0 {
			return fmt.Errorf("workload VRAM (%s) is below minimum required (%s)", workload.Spec.Resources.Requests.Vram.String(), quota.Spec.Single.Min.VRAM.String())
		}
	}

	return nil
}

// InjectDecoder injects the decoder
func (v *GPUResourceQuotaValidator) InjectDecoder(d admission.Decoder) error {
	v.decoder = d
	return nil
}

// SetupWithManager sets up the webhook with the Manager
// SetupGPUResourceQuotaWebhookWithManager registers the webhook for GPUResourceQuota validation
func SetupGPUResourceQuotaWebhookWithManager(mgr ctrl.Manager) error {
	validator := &GPUResourceQuotaValidator{
		Client:  mgr.GetClient(),
		decoder: admission.NewDecoder(mgr.GetScheme()),
	}

	webhookServer := mgr.GetWebhookServer()
	webhookServer.Register("/validate-tensor-fusion-ai-v1-tensorfusionworkload",
		&admission.Webhook{
			Handler: validator,
		})
	return nil
}

// validateWorkloadSpec validates basic workload specification
func (v *GPUResourceQuotaValidator) validateWorkloadSpec(workload *tensorfusionv1.TensorFusionWorkload) error {
	// Validate required fields
	if workload.Spec.PoolName == "" {
		return fmt.Errorf("poolName is required")
	}

	// Validate resource request reasonableness
	if workload.Spec.Resources.Requests.Tflops.IsZero() {
		return fmt.Errorf("TFlops request must be greater than 0")
	}

	if workload.Spec.Resources.Requests.Vram.IsZero() {
		return fmt.Errorf("VRAM request must be greater than 0")
	}

	// Validate replicas range
	if workload.Spec.Replicas != nil && *workload.Spec.Replicas < 0 {
		return fmt.Errorf("replicas must be non-negative")
	}

	// Note: GPUCount is uint type, so it's always non-negative by definition
	// The actual GPU allocation will be handled by the GPUAllocator

	return nil
}

// validateBusinessLogic validates business logic rules
func (v *GPUResourceQuotaValidator) validateBusinessLogic(workload *tensorfusionv1.TensorFusionWorkload) error {
	// Validate requests <= limits
	if !workload.Spec.Resources.Limits.Tflops.IsZero() &&
		workload.Spec.Resources.Limits.Tflops.Cmp(workload.Spec.Resources.Requests.Tflops) < 0 {
		return fmt.Errorf("TFlops limits must be >= requests")
	}

	if !workload.Spec.Resources.Limits.Vram.IsZero() &&
		workload.Spec.Resources.Limits.Vram.Cmp(workload.Spec.Resources.Requests.Vram) < 0 {
		return fmt.Errorf("VRAM limits must be >= requests")
	}

	// Validate QoS level
	validQoS := map[string]bool{
		"low": true, "medium": true, "high": true, "critical": true,
	}
	if workload.Spec.Qos != "" && !validQoS[string(workload.Spec.Qos)] {
		return fmt.Errorf("invalid QoS level: %s", workload.Spec.Qos)
	}

	return nil
}
