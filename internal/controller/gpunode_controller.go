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

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	tfv1 "github.com/NexusGPU/tensor-fusion-operator/api/v1"
	scheduler "github.com/NexusGPU/tensor-fusion-operator/internal/scheduler"
)

// GPUNodeReconciler reconciles a GPUNode object
type GPUNodeReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Scheduler scheduler.Scheduler
}

// +kubebuilder:rbac:groups=tensor-fusion.ai.tensor-fusion.ai,resources=gpunodes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tensor-fusion.ai.tensor-fusion.ai,resources=gpunodes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=tensor-fusion.ai.tensor-fusion.ai,resources=gpunodes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *GPUNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// TODO: Calculate tflops and update capacity here
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GPUNodeReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tfv1.GPUNode{}).
		Named("gpunode").
		WithEventFilter(
			predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					r.Scheduler.OnAdd(e.Object.(*tfv1.GPUNode))
					return true
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					r.Scheduler.OnUpdate(e.ObjectOld.(*tfv1.GPUNode), e.ObjectNew.(*tfv1.GPUNode))
					return true
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					r.Scheduler.OnDelete(e.Object.(*tfv1.GPUNode))
					return true
				},
			},
		).
		Complete(r)
}
