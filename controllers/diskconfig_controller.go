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
	"reflect"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
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
	"github.com/ondat/discoblocks/pkg/drivers"
	"github.com/ondat/discoblocks/pkg/utils"
)

const concurrency = 10

var tryLock = utils.CreateSemaphore(1, time.Second)

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

	logger.Info("Fetch PVCs...")

	label, err := labels.NewRequirement("discoblocks", selection.Equals, []string{req.Name})
	if err != nil {
		logger.Error(err, "Unable to parse PVC label selector")
		return ctrl.Result{}, nil
	}
	pvcSelector := labels.NewSelector().Add(*label)

	pvcList := corev1.PersistentVolumeClaimList{}
	if err = r.List(ctx, &pvcList, &client.ListOptions{
		Namespace:     req.Namespace,
		LabelSelector: pvcSelector,
	}); err != nil {
		logger.Info("Failed to list PVCs", "error", err.Error())
		return ctrl.Result{}, fmt.Errorf("unable to list PVCs: %w", err)
	}

	scFinalizer := utils.RenderFinalizer(req.Name, req.Namespace)

	config := discoblocksondatiov1.DiskConfig{}
	err = r.Get(ctx, req.NamespacedName, &config)
	switch {
	case err != nil && apierrors.IsNotFound(err):
		logger.Info("DiskConfig not found")

		return ctrl.Result{}, nil
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("unable to fetch DiskConfig: %w", err)
	case config.DeletionTimestamp != nil:
		logger.Info("DiskConfig delete in progress")

		config.Status.Phase = discoblocksondatiov1.Deleting
		if err = r.Client.Status().Update(ctx, &config); err != nil {
			logger.Info("Unable to update DiskConfig status", "error", err.Error())
			return ctrl.Result{}, fmt.Errorf("unable to update DiskConfig status: %w", err)
		}

		logger.Info("Fetch StrorageClasses...")

		scList := storagev1.StorageClassList{}
		if err = r.Client.List(ctx, &scList); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to list StorageClasses: %w", err)
		}

		for i := range scList.Items {
			if controllerutil.ContainsFinalizer(&scList.Items[i], scFinalizer) {
				controllerutil.RemoveFinalizer(&scList.Items[i], scFinalizer)

				//nolint:govet // logger is ok to shadowing
				logger := logger.WithValues("sc_name", scList.Items[i].Name)
				logger.Info("Remove StorageClass finalizer...", "finalizer", scFinalizer)

				if err = r.Client.Update(ctx, &scList.Items[i]); err != nil {
					logger.Info("Failed to remove finalizer of StorageClass", "error", err.Error())
					return ctrl.Result{}, fmt.Errorf("unable to remove finalizer of StorageClass: %w", err)
				}
			}
		}

		return r.reconcileDelete(ctx, req.Name, &pvcList, logger.WithValues("mode", "delete"))
	}

	logger.Info("Fetch StorageClass...")

	config.Status.Phase = discoblocksondatiov1.Running
	if err = r.Client.Status().Update(ctx, &config); err != nil {
		logger.Info("Unable to update DiskConfig status", "error", err.Error())
		return ctrl.Result{}, fmt.Errorf("unable to update DiskConfig status: %w", err)
	}

	sc := storagev1.StorageClass{}
	if err = r.Get(ctx, types.NamespacedName{Name: config.Spec.StorageClassName}, &sc); err != nil {
		if apierrors.IsNotFound(err) {
			// TODO create default storageclass
			logger.Info("StorageClass not found")
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		}

		logger.Info("Unable to fetch StorageClass", "error", err.Error())
		return ctrl.Result{}, fmt.Errorf("unable to fetch StorageClass: %w", err)
	}
	logger = logger.WithValues("sc_name", config.Spec.StorageClassName)

	if !controllerutil.ContainsFinalizer(&sc, scFinalizer) {
		controllerutil.AddFinalizer(&sc, scFinalizer)

		logger.Info("Update StorageClass finalizer...", "finalizer", scFinalizer)

		if err = r.Client.Update(ctx, &sc); err != nil {
			logger.Info("Failed to update StorageClass", "error", err.Error())
			return ctrl.Result{}, fmt.Errorf("unable to update StorageClass: %w", err)
		}
	}

	var result ctrl.Result
	result, err = r.reconcileUpdate(ctx, &config, &sc, &pvcList, logger.WithValues("mode", "update"))
	if err == nil {
		config.Status.Phase = discoblocksondatiov1.Ready
		if err = r.Client.Status().Update(ctx, &config); err != nil {
			logger.Info("Unable to update DiskConfig status", "error", err.Error())
			return ctrl.Result{}, fmt.Errorf("unable to update DiskConfig status: %w", err)
		}
	}

	return result, err
}

func (r *DiskConfigReconciler) reconcileDelete(ctx context.Context, configName string, pvcs *corev1.PersistentVolumeClaimList, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Update PVCs...")

	pvcFinalizer := utils.RenderFinalizer(configName)

	sem := utils.CreateSemaphore(concurrency, time.Nanosecond)
	errChan := make(chan error)
	wg := sync.WaitGroup{}

	for i := range pvcs.Items {
		wg.Add(1)

		i := i
		go func() {
			defer wg.Done()

			unlock, err := utils.WaitForSemaphore(ctx, sem, errChan)
			if err != nil {
				logger.Info("Context deadline")
				errChan <- fmt.Errorf("context deadline %s->%s", pvcs.Items[i].GetNamespace(), pvcs.Items[i].GetName())
			}
			defer unlock()

			if controllerutil.ContainsFinalizer(&pvcs.Items[i], pvcFinalizer) {
				controllerutil.RemoveFinalizer(&pvcs.Items[i], pvcFinalizer)

				//nolint:govet // logger is ok to shadowing
				logger := logger.WithValues("pvc_name", pvcs.Items[i].Name, "pvc_namespace", pvcs.Items[i].Namespace)
				logger.Info("Update PVC finalizer...", "finalizer", pvcFinalizer)

				if err = r.Update(ctx, &pvcs.Items[i]); err != nil {
					logger.Info("Failed to remove finalizer of PVC", "error", err.Error())
					errChan <- fmt.Errorf("unable to remove finalizer of PVC %s->%s: %w", pvcs.Items[i].Namespace, pvcs.Items[i].Name, err)
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

func (r *DiskConfigReconciler) reconcileUpdate(ctx context.Context, config *discoblocksondatiov1.DiskConfig, sc *storagev1.StorageClass, pvcs *corev1.PersistentVolumeClaimList, logger logr.Logger) (ctrl.Result, error) {
	capacity, err := resource.ParseQuantity(config.Spec.Capacity)
	if err != nil {
		logger.Error(err, "Capacity is invalid")
		return ctrl.Result{}, nil
	}

	var maxCapacity resource.Quantity
	if config.Spec.Policy.MaximumCapacityOfDisk != "" {
		maxCapacity, err = resource.ParseQuantity(config.Spec.Policy.MaximumCapacityOfDisk)
		if err != nil {
			logger.Error(err, "Max capacity is invalid")
			return ctrl.Result{}, nil
		}
	}

	driver := drivers.GetDriver(sc.Provisioner)
	if driver == nil {
		logger.Info("Driver not found", "provisioner", sc.Provisioner)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	if err = driver.IsStorageClassValid(sc); err != nil {
		logger.Info("Invalid StorageClass", "error", err.Error())
		return ctrl.Result{RequeueAfter: time.Minute}, fmt.Errorf("invalid StorageClass: %w", err)
	}

	if len(pvcs.Items) == 0 {
		return ctrl.Result{}, nil
	}

	logger.Info("Update PVCs...")

	sem := utils.CreateSemaphore(concurrency, time.Nanosecond)
	errChan := make(chan error)
	wg := sync.WaitGroup{}

	for i := range pvcs.Items {
		if !controllerutil.ContainsFinalizer(&pvcs.Items[i], utils.RenderFinalizer(pvcs.Items[i].Labels["discoblocks"])) {
			logger.Info("PVC not managed by", "config", pvcs.Items[i].Labels["discoblocks"])

			continue
		}

		wg.Add(1)

		i := i
		go func() {
			defer wg.Done()

			unlock, err := utils.WaitForSemaphore(ctx, sem, errChan)
			if err != nil {
				logger.Info("Context deadline")
				errChan <- fmt.Errorf("context deadline %s->%s", pvcs.Items[i].GetNamespace(), pvcs.Items[i].GetName())
			}
			defer unlock()

			if maxCapacity.CmpInt64(0) != 0 && maxCapacity.Cmp(capacity) == -1 {
				logger.Info("Disk is not increaseable", "max_capacity", config.Spec.Policy.MaximumCapacityOfDisk)

				// TODO implement new disk create

				return
			}

			pvcs.Items[i].Spec.Resources.Requests[corev1.ResourceStorage] = capacity

			//nolint:govet // logger is ok to shadowing
			logger := logger.WithValues("pvc_name", pvcs.Items[i].Name, "pvc_namespace", pvcs.Items[i].Namespace)
			logger.Info("Update PVC...")

			if err = r.Update(ctx, &pvcs.Items[i]); err != nil {
				logger.Info("Failed to update PVC", "error", err.Error())
				errChan <- fmt.Errorf("unable to update PVC %s->%s: %w", pvcs.Items[i].Namespace, pvcs.Items[i].Name, err)
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

type diskConfigEventFilter struct {
	logger logr.Logger
}

func (ef diskConfigEventFilter) Create(_ event.CreateEvent) bool {
	return true
}

func (ef diskConfigEventFilter) Delete(_ event.DeleteEvent) bool {
	return false
}

func (ef diskConfigEventFilter) Update(e event.UpdateEvent) bool {
	newObj, ok := e.ObjectNew.(*discoblocksondatiov1.DiskConfig)
	if !ok {
		ef.logger.Error(errors.New("unsupported type"), "Unable to cast new object")
		return false
	}
	oldObj, ok := e.ObjectOld.(*discoblocksondatiov1.DiskConfig)
	if !ok {
		ef.logger.Error(errors.New("unsupported type"), "Unable to cast old object")
		return false
	}

	return newObj.DeletionTimestamp != nil || !reflect.DeepEqual(oldObj.Spec, newObj.Spec)
}

func (ef diskConfigEventFilter) Generic(_ event.GenericEvent) bool {
	return false
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
