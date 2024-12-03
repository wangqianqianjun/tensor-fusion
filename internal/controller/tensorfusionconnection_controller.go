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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	tfv1 "github.com/NexusGPU/tensor-fusion-operator/api/v1"
	scheduler "github.com/NexusGPU/tensor-fusion-operator/internal/scheduler"
	"github.com/NexusGPU/tensor-fusion-operator/internal/worker"
)

// TensorFusionConnectionReconciler reconciles a TensorFusionConnection object
type TensorFusionConnectionReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Scheduler scheduler.Scheduler
}

// +kubebuilder:rbac:groups=tensor-fusion.ai.tensor-fusion.ai,resources=tensorfusionconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tensor-fusion.ai.tensor-fusion.ai,resources=tensorfusionconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=tensor-fusion.ai.tensor-fusion.ai,resources=tensorfusionconnections/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *TensorFusionConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Get the TensorFusionConnection object
	connection := &tfv1.TensorFusionConnection{}
	if err := r.Get(ctx, req.NamespacedName, connection); err != nil {
		if errors.IsNotFound(err) {
			// Object not found, could have been deleted after reconcile request, return without error
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get TensorFusionConnection")
		return ctrl.Result{}, err
	}

	var node *tfv1.GPUNode
	// If status is not set or pending, try to schedule
	if connection.Status.Phase == "" || connection.Status.Phase == tfv1.TensorFusionConnectionPending {
		// Try to get an available node from scheduler
		node, err := r.Scheduler.Schedule(connection.Spec.Resources.Request)
		if err != nil {
			log.Error(err, "Failed to schedule connection")
			connection.Status.Phase = tfv1.TensorFusionConnectionPending
		} else if node != nil {
			connection.Status.Phase = tfv1.TensorFusionConnectionRunning
			connection.Status.ConnectionURL = worker.GenerateConnectionURL(node, connection)
		} else {
			connection.Status.Phase = tfv1.TensorFusionConnectionPending
		}
	}

	if err := r.MustUpdateStatus(ctx, connection, node); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *TensorFusionConnectionReconciler) MustUpdateStatus(ctx context.Context, connection *tfv1.TensorFusionConnection, gpuNode *tfv1.GPUNode) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// Get the latest version of the connection
		latestConnection := &tfv1.TensorFusionConnection{}
		if err := r.Get(ctx, client.ObjectKey{
			Name:      connection.Name,
			Namespace: connection.Namespace,
		}, latestConnection); err != nil {
			return err
		}

		// Update the status fields we care about
		latestConnection.Status.Phase = connection.Status.Phase
		latestConnection.Status.ConnectionURL = connection.Status.ConnectionURL

		// Update the connection status
		if err := r.Status().Update(ctx, latestConnection); err != nil {
			return err
		}

		if gpuNode != nil {
			// Get the latest version of the node
			latestNode := &tfv1.GPUNode{}

			if err := r.Get(ctx, client.ObjectKey{
				Name:      gpuNode.Name,
				Namespace: gpuNode.Namespace,
			}, latestNode); err != nil {
				return err
			}

			// Update the status fields we care about
			latestNode.Status.Available = gpuNode.Status.Available
			if err := r.Status().Update(ctx, latestNode); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *TensorFusionConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tfv1.TensorFusionConnection{}).
		Named("tensorfusionconnection").
		Complete(r)
}
