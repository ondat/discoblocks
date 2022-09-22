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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/utils"
)

const concurrency = 10

var controllerSemaphore = utils.CreateSemaphore(1, time.Second)

// DiskConfigReconciler reconciles a DiskConfig object
type DiskConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

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
	logger := log.FromContext(ctx).WithName("DiskConfigReconciler").WithValues("name", req.Name, "namespace", req.Name)

	lock, unlock := controllerSemaphore()
	if !lock {
		logger.Info("Another operation is on going, event needs to be resceduled")
		return ctrl.Result{Requeue: true}, nil
	}
	defer unlock()

	logger.Info("Reconciling...")
	defer logger.Info("Reconciled")

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	config := discoblocksondatiov1.DiskConfig{}
	err := r.Get(ctx, req.NamespacedName, &config)
	switch {
	case err != nil && apierrors.IsNotFound(err):
		logger.Info("DiskConfig not found")

		return r.reconcileDelete(ctx, req.Name, req.Namespace, logger.WithValues("mode", "delete"))
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("unable to fetch DiskConfig: %w", err)
	case config.DeletionTimestamp != nil:
		logger.Info("DiskConfig delete in progress")

		logger.Info("Update phase to Deleting...")

		config.Status.Phase = discoblocksondatiov1.Deleting
		if err = r.Client.Status().Update(ctx, &config); err != nil {
			logger.Info("Unable to update DiskConfig status", "error", err.Error())
			return ctrl.Result{}, fmt.Errorf("unable to update DiskConfig status: %w", err)
		}

		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	logger.Info("Update phase to Running...")

	config.Status.Phase = discoblocksondatiov1.Running
	if err = r.Client.Status().Update(ctx, &config); err != nil {
		logger.Info("Unable to update DiskConfig status", "error", err.Error())
		return ctrl.Result{}, fmt.Errorf("unable to update DiskConfig status: %w", err)
	}

	var result ctrl.Result
	result, err = r.reconcileUpdate(ctx, &config, logger.WithValues("mode", "update"))
	if err == nil {
		logger.Info("Update phase to Ready...")

		config.Status.Phase = discoblocksondatiov1.Ready
		if err = r.Client.Status().Update(ctx, &config); err != nil {
			logger.Info("Unable to update DiskConfig status", "error", err.Error())
			return ctrl.Result{}, fmt.Errorf("unable to update DiskConfig status: %w", err)
		}
	}

	return result, err
}

func (r *DiskConfigReconciler) reconcileDelete(ctx context.Context, configName, configNamespace string, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Fetch StrorageClasses...")

	scList := storagev1.StorageClassList{}
	if err := r.Client.List(ctx, &scList); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to list StorageClasses: %w", err)
	}

	scFinalizer := utils.RenderFinalizer(configName, configNamespace)

	for i := range scList.Items {
		if scList.Items[i].DeletionTimestamp != nil || !controllerutil.ContainsFinalizer(&scList.Items[i], scFinalizer) {
			continue
		}

		controllerutil.RemoveFinalizer(&scList.Items[i], scFinalizer)

		logger := logger.WithValues("sc_name", scList.Items[i].Name)
		logger.Info("Remove StorageClass finalizer...", "finalizer", scFinalizer)

		if err := r.Client.Update(ctx, &scList.Items[i]); err != nil {
			logger.Info("Failed to remove finalizer of StorageClass", "error", err.Error())
			return ctrl.Result{}, fmt.Errorf("unable to remove finalizer of StorageClass: %w", err)
		}
	}

	finalizer := utils.RenderFinalizer(configName)

	logger.Info("Update PVCs...")

	sem := utils.CreateSemaphore(concurrency, time.Nanosecond)
	errChan := make(chan error)
	wg := sync.WaitGroup{}

	logger.Info("Fetch PVCs...")

	label, err := labels.NewRequirement("discoblocks", selection.Equals, []string{configName})
	if err != nil {
		logger.Error(err, "Unable to parse PVC label selector")
		return ctrl.Result{}, nil
	}
	pvcSelector := labels.NewSelector().Add(*label)

	pvcList := corev1.PersistentVolumeClaimList{}
	if err = r.List(ctx, &pvcList, &client.ListOptions{
		Namespace:     configNamespace,
		LabelSelector: pvcSelector,
	}); err != nil {
		logger.Info("Failed to list PVCs", "error", err.Error())
		return ctrl.Result{}, fmt.Errorf("unable to list PVCs: %w", err)
	}

	for i := range pvcList.Items {
		if !controllerutil.ContainsFinalizer(&pvcList.Items[i], utils.RenderFinalizer(pvcList.Items[i].Labels["discoblocks"])) {
			logger.Info("PVC not managed by", "config", pvcList.Items[i].Labels["discoblocks"])
			continue
		}

		wg.Add(1)

		i := i
		go func() {
			defer wg.Done()

			unlock, err := utils.WaitForSemaphore(ctx, sem, errChan)
			if err != nil {
				logger.Info("Context deadline")
				errChan <- fmt.Errorf("context deadline %s->%s", pvcList.Items[i].GetNamespace(), pvcList.Items[i].GetName())
				return
			}
			defer unlock()

			if controllerutil.ContainsFinalizer(&pvcList.Items[i], finalizer) {
				controllerutil.RemoveFinalizer(&pvcList.Items[i], finalizer)
				logger := logger.WithValues("pvc_name", pvcList.Items[i].Name, "pvc_namespace", pvcList.Items[i].Namespace)
				logger.Info("Update PVC finalizer...", "finalizer", finalizer)

				if err = r.Update(ctx, &pvcList.Items[i]); err != nil {
					logger.Info("Failed to remove finalizer of PVC", "error", err.Error())
					errChan <- fmt.Errorf("unable to remove finalizer of PVC %s->%s: %w", pvcList.Items[i].Namespace, pvcList.Items[i].Name, err)
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(errChan)
	}()

	errs := []string{}

	for {
		err, ok := <-errChan
		if !ok {
			if len(errs) != 0 {
				return ctrl.Result{}, fmt.Errorf("some PVC updates failed: %s", strings.Join(errs, "\t"))
			}

			return ctrl.Result{}, nil
		}

		errs = append(errs, err.Error())
	}
}

func (r *DiskConfigReconciler) reconcileUpdate(ctx context.Context, config *discoblocksondatiov1.DiskConfig, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Fetch StorageClass...")

	sc := storagev1.StorageClass{}
	if err := r.Get(ctx, types.NamespacedName{Name: config.Spec.StorageClassName}, &sc); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("StorageClass not found")
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		}

		logger.Info("Unable to fetch StorageClass", "error", err.Error())
		return ctrl.Result{}, fmt.Errorf("unable to fetch StorageClass: %w", err)
	}
	logger = logger.WithValues("sc_name", config.Spec.StorageClassName)

	scFinalizer := utils.RenderFinalizer(config.Name, config.Namespace)

	if !controllerutil.ContainsFinalizer(&sc, scFinalizer) {
		controllerutil.AddFinalizer(&sc, scFinalizer)

		logger.Info("Update StorageClass finalizer...", "finalizer", scFinalizer)

		if err := r.Client.Update(ctx, &sc); err != nil {
			logger.Info("Failed to update StorageClass", "error", err.Error())
			return ctrl.Result{}, fmt.Errorf("unable to update StorageClass: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DiskConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&discoblocksondatiov1.DiskConfig{}).
		WithEventFilter(diskConfigEventFilter{logger: mgr.GetLogger().WithName("DiskConfigReconciler")}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}

type diskConfigEventFilter struct {
	logger logr.Logger
}

func (ef diskConfigEventFilter) Create(_ event.CreateEvent) bool {
	return true
}

func (ef diskConfigEventFilter) Delete(_ event.DeleteEvent) bool {
	return true
}

func (ef diskConfigEventFilter) Update(e event.UpdateEvent) bool {
	oldObj, ok := e.ObjectOld.(*discoblocksondatiov1.DiskConfig)
	if !ok {
		ef.logger.Error(errors.New("unsupported type"), "Unable to cast old object")
		return false
	}

	newObj, ok := e.ObjectNew.(*discoblocksondatiov1.DiskConfig)
	if !ok {
		ef.logger.Error(errors.New("unsupported type"), "Unable to cast new object")
		return false
	}

	newRawSpec, err := json.Marshal(newObj.Spec)
	if err != nil {
		ef.logger.Error(errors.New("invalid content"), "Unable to marshal new object")
		return false
	}

	oldRawSpec, err := json.Marshal(oldObj.Spec)
	if err != nil {
		ef.logger.Error(errors.New("invalid content"), "Unable to marshal old object")
		return false
	}

	return !bytes.Equal(newRawSpec, oldRawSpec)
}

func (ef diskConfigEventFilter) Generic(_ event.GenericEvent) bool {
	return false
}
