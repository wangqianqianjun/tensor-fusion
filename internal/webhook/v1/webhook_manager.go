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
	"github.com/NexusGPU/tensor-fusion/internal/portallocator"
	ctrl "sigs.k8s.io/controller-runtime"
)

// WebhookManager manages webhook registration centrally
type WebhookManager struct {
	manager       ctrl.Manager
	portAllocator *portallocator.PortAllocator
}

// NewWebhookManager creates a new webhook manager
func NewWebhookManager(mgr ctrl.Manager, portAllocator *portallocator.PortAllocator) *WebhookManager {
	return &WebhookManager{
		manager:       mgr,
		portAllocator: portAllocator,
	}
}

// SetupAllWebhooks sets up all webhooks
func (wm *WebhookManager) SetupAllWebhooks() error {
	// Register Pod Mutating Webhook
	if err := SetupPodWebhookWithManager(wm.manager, wm.portAllocator); err != nil {
		return err
	}

	// Register GPUResourceQuota Validating Webhook
	if err := SetupGPUResourceQuotaWebhookWithManager(wm.manager); err != nil {
		return err
	}

	// Future webhooks can be added here
	// if err := SetupGPUPoolWebhookWithManager(wm.manager); err != nil {
	//     return err
	// }

	return nil
}
