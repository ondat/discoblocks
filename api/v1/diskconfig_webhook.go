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
	"regexp"
	"strings"
	"time"

	"github.com/ondat/discoblocks/pkg/drivers"
	"github.com/ondat/discoblocks/pkg/metrics"
	"golang.org/x/net/context"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package
var diskConfigLog = logf.Log.WithName("v1.DiskConfigWebhook")

var reservedCharacters = regexp.MustCompile(`[>|<|||:|&|.|\+|\*|!|\?|\^|\$|\(|\)|\[|\]|\{|\}]`)

// SetupWebhookWithManager sets up the webhook with the Manager.
func (r *DiskConfig) SetupWebhookWithManager(mgr ctrl.Manager) error {
	if diskConfigWebhookDependencies == nil {
		return errors.New("dependencies are missing")
	}

	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/validate-discoblocks-ondat-io-v1-diskconfig,mutating=false,failurePolicy=fail,sideEffects=None,groups=discoblocks.ondat.io,resources=diskconfigs,verbs=create;update,versions=v1,name=validatediskconfig.kb.io,admissionReviewVersions=v1

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
	logger := diskConfigLog.WithValues("dc_name", r.Name, "namespace", r.Namespace)

	logger.Info("Validate update...")
	defer logger.Info("Validated")

	if r.Spec.StorageClassName == "" {
		logger.Info("StorageClass name is invalid")
		return errors.New("invalid StorageClass name")
	}

	if r.Spec.Policy.MaximumCapacityOfDisk.CmpInt64(0) != 0 && r.Spec.Policy.MaximumCapacityOfDisk.Cmp(r.Spec.Capacity) == -1 {
		logger.Info("Capacity is more then max")
		return errors.New("invalid new capacity, more then max")
	}

	if err := validateMountPattern(r.Spec.MountPointPattern); err != nil {
		logger.Info("Invalid mount pattern", "error", err.Error())
		return err
	}

	const ten = 10
	if r.Spec.Policy.CoolDown.Duration < ten*time.Second {
		err := fmt.Errorf("minimum cool down is %d seconds", ten)
		logger.Info("Invalid cool down", "error", err.Error())
		return err
	}

	if old != nil {
		oldDC, ok := old.(*DiskConfig)
		if !ok {
			err := errors.New("invalid old object")
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

		if oldDC.Spec.Capacity.CmpInt64(0) != 0 && oldDC.Spec.Capacity.Cmp(r.Spec.Capacity) == 1 {
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
	if err := diskConfigWebhookDependencies.client.Get(ctx, types.NamespacedName{Name: r.Spec.StorageClassName}, &sc); err != nil {
		metrics.NewError("StorageClass", r.Spec.StorageClassName, "", "Kube API", "get")

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
		metrics.NewError("CSI", sc.Provisioner, "", sc.Provisioner, "GetDriver")

		logger.Info("Driver not found")
		return errors.New("driver not found")
	}

	valid, err := driver.IsStorageClassValid(&sc)
	if err != nil {
		metrics.NewError("CSI", sc.Name, "", sc.Provisioner, "IsStorageClassValid")

		logger.Error(err, "Failed to call driver", "method", "IsStorageClassValid")
		return fmt.Errorf("failed to call driver: %w", err)
	} else if !valid {
		logger.Info("Invalid StorageClass", "error", err.Error())
		return fmt.Errorf("invalid StorageClass: %w", err)
	}

	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *DiskConfig) ValidateDelete() error {
	return nil
}

func validateMountPattern(pattern string) error {
	if strings.Count(pattern, "%d") > 1 {
		return errors.New("invalid mount pattern, only one %d allowed")
	}

	if reservedCharacters.MatchString(pattern) {
		return errors.New("invalid mount pattern, contains reserved characters")
	}

	return nil
}
