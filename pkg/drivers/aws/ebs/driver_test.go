package ebs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateStorageClass(t *testing.T) {
	t.Parallel()

	bindingWait := storagev1.VolumeBindingWaitForFirstConsumer
	bindingImmediate := storagev1.VolumeBindingImmediate
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
				VolumeBindingMode:    &bindingWait,
				AllowVolumeExpansion: &ok,
			},
		},
		"wrong binding": {
			storageClass: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass",
				},
				Provisioner:       "ebs.csi.aws.com",
				VolumeBindingMode: &bindingImmediate,
			},
			expexted: errBinding,
		},
		"empty expansion": {
			storageClass: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass",
				},
				Provisioner:       "ebs.csi.aws.com",
				VolumeBindingMode: &bindingWait,
			},
			expexted: errExpanding,
		},
		"wrong expansion": {
			storageClass: storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "storageclass",
				},
				Provisioner:          "ebs.csi.aws.com",
				VolumeBindingMode:    &bindingWait,
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
