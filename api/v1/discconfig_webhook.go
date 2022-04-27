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

	"github.com/ondat/discoblocks/pkg/utils"
	"golang.org/x/net/context"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var diskConfiglog = logf.Log.WithName("diskConfig-resource")

type DiskConfigWebhook struct {
	DiskConfig
	provisioners map[string]bool
	client       client.Client
}

func (r *DiskConfigWebhook) SetupWebhookWithManager(mgr ctrl.Manager, provisioners []string) error {
	r.client = mgr.GetClient()

	r.provisioners = make(map[string]bool, len(provisioners))
	for _, p := range provisioners {
		r.provisioners[p] = true
	}

	return ctrl.NewWebhookManagedBy(mgr).
		For(&r.DiskConfig).
		Complete()
}

//+kubebuilder:webhook:path=/validate-discoblocks-ondat-io-discoblocks-ondat-io-v1-diskConfig,mutating=false,failurePolicy=fail,sideEffects=None,groups=discoblocks.ondat.io.discoblocks.ondat.io,resources=diskConfigs,verbs=create;update;delete,versions=v1,name=vdiskConfig.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &DiskConfigWebhook{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *DiskConfigWebhook) ValidateCreate() error {
	diskConfiglog.Info("validate create", "name", r.Name)
	return r.validate(nil)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *DiskConfigWebhook) ValidateUpdate(old runtime.Object) error {
	diskConfiglog.Info("validate update", "name", r.Name)

	return errors.New("update of DiskConfig not allowed")
}

func (r *DiskConfigWebhook) validate(old runtime.Object) error {
	diskConfiglog.Info("validate update", "name", r.Name)

	newSize, newUnit, err := utils.ParseCapacity(r.Spec.Capacity)
	if err != nil {
		return errors.New("invalid new capacity")
	}

	if old != nil {
		oldDC, ok := old.(*DiskConfig)
		if !ok {
			return errors.New("invalid old object")
		}

		if oldDC.Spec.StorageClassName != r.Spec.StorageClassName {
			return errors.New("storageclass name is immutable field")
		}

		if oldDC.Spec.MountPointPattern != r.Spec.MountPointPattern {
			return errors.New("mount point pattern is immutable field")
		}

		// TODO proper calculation would be nice.
		oldSize, oldUnit, err := utils.ParseCapacity(oldDC.Spec.Capacity)
		if err != nil {
			return errors.New("invalid old capacity")
		}

		if oldUnit != newUnit {
			return errors.New("capacity unit is immutable")
		} else if oldSize > newSize {
			return errors.New("shrinking disk is not supported")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	sc := storagev1.StorageClass{}
	if err = r.client.Get(ctx, types.NamespacedName{Name: r.Spec.StorageClassName}, &sc); err != nil {
		// TODO not found makes sense to handle separatly, and some retry
		return fmt.Errorf("unable to fetch StorageClass: %w", err)
	}

	if _, ok := r.provisioners[sc.Provisioner]; !ok {
		return errors.New("provisioner not supported")
	}

	if sc.ReclaimPolicy == nil || *sc.ReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
		return errors.New("only reclaimPolicy Retain is supported")
	}

	if sc.VolumeBindingMode != nil && *sc.VolumeBindingMode != storagev1.VolumeBindingImmediate {
		return errors.New("only volumeBindingMode Immediate is supported")
	}

	// TODO validate CSI deployment

	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *DiskConfigWebhook) ValidateDelete() error {
	diskConfiglog.Info("validate delete", "name", r.Name)

	return errors.New("deletion of DiskConfig not allowed")
}
