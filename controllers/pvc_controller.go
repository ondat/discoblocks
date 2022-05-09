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
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PVCReconciler reconciles a PVC object
type PVCReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// Modify the Reconcile function to compare the state specified by
// the PVC object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
func (r *PVCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("PVCReconciler").WithValues("name", req.Name, "namespace", req.Name)

	lock, unlock := tryLock()
	if !lock {
		logger.Info("Another operation is on going, event needs to be resceduled")
		return ctrl.Result{Requeue: true}, nil
	}
	defer unlock()

	logger.Info("Reconciling...")
	defer logger.Info("Reconciled")

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	pvc := corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, req.NamespacedName, &pvc)
	switch {
	case err != nil && apierrors.IsNotFound(err):
		logger.Info("PVC not found")

		return ctrl.Result{}, nil
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("unable to fetch PVC: %w", err)
	case !controllerutil.ContainsFinalizer(&pvc, utils.RenderFinalizer(pvc.Labels["discoblocks"])):
		logger.Info("PVC not managed by", "config", pvc.Labels["discoblocks"])

		return ctrl.Result{}, nil
	}

	logger.Info("Fetch DiskConfig...")

	config := discoblocksondatiov1.DiskConfig{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Labels["discoblocks"]}, &config); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("PVC not found")

			return ctrl.Result{}, nil
		}

		logger.Info("Unable to fetch PVC", "error", err.Error())
		return ctrl.Result{}, errors.New("unable to fetch PVC")
	}
	logger = logger.WithValues("dc_name", config.Name)

	if pvc.DeletionTimestamp != nil {
		if _, ok := config.Status.PersistentVolumeClaims[config.Name]; ok {
			logger.Info("Remove status")
			delete(config.Status.PersistentVolumeClaims[config.Name], pvc.Name)
		}

		if len(config.Status.PersistentVolumeClaims[config.Name]) == 0 {
			delete(config.Status.PersistentVolumeClaims, config.Name)
		}
	} else {
		if config.Status.PersistentVolumeClaims == nil {
			config.Status.PersistentVolumeClaims = map[string]map[string]corev1.PersistentVolumeClaimPhase{}
		}
		if config.Status.PersistentVolumeClaims[config.Name] == nil {
			config.Status.PersistentVolumeClaims[config.Name] = map[string]corev1.PersistentVolumeClaimPhase{}
		}

		logger.Info("Add status", "phase", pvc.Status.Phase)
		config.Status.PersistentVolumeClaims[config.Name][pvc.Name] = pvc.Status.Phase
	}

	// TODO update conditions

	logger.Info("Updating PVC...")

	if err := r.Client.Status().Update(ctx, &config); err != nil {
		logger.Info("Unable to update PVC status", "error", err.Error())
		return ctrl.Result{}, errors.New("unable to update PVC status")
	}

	logger.Info("Updated")

	return ctrl.Result{}, nil
}

type pvcEventFilter struct {
	logger logr.Logger
}

func (ef pvcEventFilter) Create(_ event.CreateEvent) bool {
	return true
}

func (ef pvcEventFilter) Delete(_ event.DeleteEvent) bool {
	return false
}

func (ef pvcEventFilter) Update(e event.UpdateEvent) bool {
	newObj, ok := e.ObjectNew.(*corev1.PersistentVolumeClaim)
	if !ok {
		ef.logger.Error(errors.New("unsupported type"), "Unable to cast new object")
		return false
	}
	oldObj, ok := e.ObjectOld.(*corev1.PersistentVolumeClaim)
	if !ok {
		ef.logger.Error(errors.New("unsupported type"), "Unable to cast old object")
		return false
	}

	return newObj.DeletionTimestamp != nil || oldObj.Status.Phase != newObj.Status.Phase
}

func (ef pvcEventFilter) Generic(_ event.GenericEvent) bool {
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *PVCReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		WithEventFilter(pvcEventFilter{logger: mgr.GetLogger().WithName("PVCReconciler")}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}
