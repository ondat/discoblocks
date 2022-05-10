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

package v1

import (
	"errors"
	"fmt"
	"time"

	"github.com/ondat/discoblocks/pkg/drivers"
	"golang.org/x/net/context"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package
var diskConfigLog = logf.Log.WithName("DiskConfigWebhook")

// SetupWebhookWithManager sets up the webhook with the Manager.
func (r *DiskConfig) SetupWebhookWithManager(mgr ctrl.Manager) error {
	if diskConfigWebhookDependencies == nil {
		return errors.New("dependencies are missing")
	}

	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/validate-discoblocks-ondat-io-v1-diskconfig,mutating=false,failurePolicy=fail,sideEffects=None,groups=discoblocks.ondat.io,resources=diskconfigs,verbs=create;update;delete,versions=v1,name=validatediskconfig.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &DiskConfig{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *DiskConfig) ValidateCreate() error {
	return r.validate(nil)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *DiskConfig) ValidateUpdate(old runtime.Object) error {
	return r.validate(old)
}

func (r *DiskConfig) validate(old runtime.Object) error {
	logger := diskConfigLog.WithValues("name", r.Name, "namespace", r.Namespace)

	logger.Info("Validate update...")
	defer logger.Info("Validated")

	// TODO remove once we generate detault
	if r.Spec.StorageClassName == "" {
		logger.Info("StorageClass name is invalid")
		return errors.New("invalid StorageClass name")
	}

	if _, err := resource.ParseQuantity(r.Spec.Policy.MaximumCapacityOfDisk); err != nil {
		logger.Info("Max capacity is invalid")
		return errors.New("invalid max capacity")
	}

	newCapacity, err := resource.ParseQuantity(r.Spec.Capacity)
	if err != nil {
		logger.Info("Capacity is invalid")
		return errors.New("invalid new capacity")
	}

	maxCapacity, err := resource.ParseQuantity(r.Spec.Policy.MaximumCapacityOfDisk)
	if err != nil {
		logger.Info("Max capacity is invalid")
		return errors.New("invalid max capacity")
	}

	if maxCapacity.CmpInt64(0) != 0 && maxCapacity.Cmp(newCapacity) == -1 {
		logger.Info("Capacity is more then max")
		return errors.New("invalid new capacity, more then max")
	}

	if old != nil {
		oldDC, ok := old.(*DiskConfig)
		if !ok {
			err = errors.New("invalid old object")
			logger.Error(err, "this should not happen")
			return err
		}

		if oldDC.Spec.StorageClassName != r.Spec.StorageClassName {
			logger.Info("Name of StorageClass is immutable")
			return errors.New("storageclass name is immutable field")
		}

		if oldDC.Spec.MountPointPattern != r.Spec.MountPointPattern {
			logger.Info("Mount pattern of StorageClass is immutable")
			return errors.New("mount point pattern is immutable field")
		}

		var oldCapacity resource.Quantity
		oldCapacity, err = resource.ParseQuantity(oldDC.Spec.Capacity)
		if err != nil {
			err = errors.New("invalid old capacity")
			logger.Error(err, "this should not happen")
			return err
		}

		if oldCapacity.CmpInt64(0) != 0 && oldCapacity.Cmp(newCapacity) == 1 {
			logger.Info("Shrinking disk is not supported")
			return errors.New("shrinking disk is not supported")
		}
	}

	if r.Spec.NodeSelector != nil && len(r.Spec.NodeSelector.MatchExpressions) != 0 {
		logger.Info("Node selector matchExpressions not supported")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	logger = logger.WithValues("sc_name", r.Spec.StorageClassName)
	logger.Info("Fetch StorageClass...")

	sc := storagev1.StorageClass{}
	if err = diskConfigWebhookDependencies.client.Get(ctx, types.NamespacedName{Name: r.Spec.StorageClassName}, &sc); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("StorageClass not found")
		} else {
			logger.Error(err, "Unable to fetch StorageClass")
		}
		return fmt.Errorf("unable to fetch StorageClass: %w", err)
	}
	logger = logger.WithValues("provisioner", sc.Provisioner)

	if _, ok := diskConfigWebhookDependencies.provisioners[sc.Provisioner]; !ok {
		logger.Info("Provisioner not supported")
		return errors.New("provisioner not supported")
	}

	driver := drivers.GetDriver(sc.Provisioner)
	if driver == nil {
		logger.Info("Driver not found")
		return errors.New("driver not found")
	}

	if err = driver.IsStorageClassValid(&sc); err != nil {
		logger.Info("Invalid StorageClass", "error", err.Error())
		return fmt.Errorf("invalid StorageClass: %w", err)
	}

	// TODO validate CSI deployment

	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *DiskConfig) ValidateDelete() error {
	diskConfigLog.Info("validate delete", "name", r.Name)

	return nil
}
