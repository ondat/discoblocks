package ebs

import (
	"errors"
	"fmt"

	"github.com/ondat/discoblocks/pkg/drivers"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"sigs.k8s.io/yaml"
)

// TODO maybe a config map of templates makes more sense then copy them to image
const pvcTemplate = `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: %s
  resources:
    requests:
      storage: 1Gi`

var (
	errRetain    = errors.New("only reclaimPolicy Retain is supported")
	errBinding   = errors.New("only volumeBindingMode WaitForFirstConsumer is supported")
	errExpanding = errors.New("only allowVolumeExpansion true is supported")
)

func init() {
	drivers.SetDriver("ebs.csi.aws.com", driver{})
}

type driver struct {
}

// IsStorageClassValid validates StorageClass
func (d driver) IsStorageClassValid(sc *storagev1.StorageClass) error {
	if sc.ReclaimPolicy == nil || *sc.ReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
		return errRetain
	}

	// TODO support Immediate requires per volume StorageClass because of topology,
	// it avoids our scheduler, because PV are in place at scheduling time
	if sc.VolumeBindingMode != nil && *sc.VolumeBindingMode != storagev1.VolumeBindingWaitForFirstConsumer {
		return errBinding
	}

	if sc.AllowVolumeExpansion == nil || !*sc.AllowVolumeExpansion {
		return errExpanding
	}

	return nil
}

// GetPVC returns the PVC
func (d driver) GetPVCStub(name, namespace, storageClassName string) (*corev1.PersistentVolumeClaim, error) {
	template := fmt.Sprintf(pvcTemplate, name, namespace, storageClassName)

	pvc := corev1.PersistentVolumeClaim{}
	if err := yaml.Unmarshal([]byte(template), &pvc); err != nil {
		return nil, err
	}

	return &pvc, nil
}
