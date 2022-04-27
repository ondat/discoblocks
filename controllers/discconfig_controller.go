/*
Copyright 2022.

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

package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
)

// DiskConfigReconciler reconciles a DiskConfig object
type DiskConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=discoblocks.ondat.io.discoblocks.ondat.io,resources=diskConfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=discoblocks.ondat.io.discoblocks.ondat.io,resources=diskConfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=discoblocks.ondat.io.discoblocks.ondat.io,resources=diskConfigs/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;create;update
//+kubebuilder:rbac:groups="",resources=persistentvolumeclaims/finalizers,verbs=update
//+kubebuilder:rbac:groups="storage.k8s.io",resources=storageclasses,verbs=get
//+kubebuilder:rbac:groups="storage.k8s.io",resources=storageclasses/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// Modify the Reconcile function to compare the state specified by
// the DiskConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *DiskConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("DiskConfigReconciler")

	if locked := lock.TryLock(); !locked {
		logger.Info("Another operation is on going, event needs to be resceduled")
		return ctrl.Result{Requeue: true}, nil
	}
	defer lock.Unlock()

	// storage class finalizer

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DiskConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&discoblocksondatiov1.DiskConfig{}).
		Complete(r)
}
