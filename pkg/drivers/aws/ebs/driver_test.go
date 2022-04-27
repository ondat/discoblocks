package ebs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateStorageClass(t *testing.T) {
	t.Parallel()

	reclaimRetain := corev1.PersistentVolumeReclaimRetain
	reclaimDelete := corev1.PersistentVolumeReclaimDelete
	bindingImmediate := storagev1.VolumeBindingImmediate
	bindingWait := storagev1.VolumeBindingWaitForFirstConsumer
	ok := true
	nok := false

	cases := map[string]struct {
		storageClass storagev1.StorageClass
		expexted     error
	}{
		"ok": {
			storageClass: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass",
				},
				Provisioner:          "ebs.csi.aws.com",
				ReclaimPolicy:        &reclaimRetain,
				VolumeBindingMode:    &bindingImmediate,
				AllowVolumeExpansion: &ok,
			},
		},
		"wrong recailm": {
			storageClass: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass",
				},
				Provisioner:       "ebs.csi.aws.com",
				ReclaimPolicy:     &reclaimDelete,
				VolumeBindingMode: &bindingImmediate,
			},
			expexted: errRetain,
		},
		"wrong binding": {
			storageClass: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass",
				},
				Provisioner:       "ebs.csi.aws.com",
				ReclaimPolicy:     &reclaimRetain,
				VolumeBindingMode: &bindingWait,
			},
			expexted: errBinding,
		},
		"empty expansion": {
			storageClass: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass",
				},
				Provisioner:       "ebs.csi.aws.com",
				ReclaimPolicy:     &reclaimRetain,
				VolumeBindingMode: &bindingImmediate,
			},
			expexted: errExpanding,
		},
		"wrong expansion": {
			storageClass: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass",
				},
				Provisioner:          "ebs.csi.aws.com",
				ReclaimPolicy:        &reclaimRetain,
				VolumeBindingMode:    &bindingImmediate,
				AllowVolumeExpansion: &nok,
			},
			expexted: errExpanding,
		},
	}

	for n, c := range cases {
		c := c
		t.Run(n, func(t *testing.T) {
			t.Parallel()

			err := (driver{}).IsStorageClassValid(&c.storageClass)

			assert.Equal(t, c.expexted, err, "invalid validation")
		})
	}
}

func TestGetPVCStub(t *testing.T) {
	_, err := (driver{}).GetPVCStub("name", "namespace", "storageclass")

	assert.Nil(t, err, "invalid PVC template")
}
