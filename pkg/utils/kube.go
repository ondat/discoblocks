package utils

import (
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/drivers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// NewPVC constructs a new PVC instance
func NewPVC(config *discoblocksondatiov1.DiskConfig, provisioner string, finalizer bool, logger logr.Logger) (*corev1.PersistentVolumeClaim, error) {
	preFix := config.CreationTimestamp.String()
	if config.Spec.AvailabilityMode != discoblocksondatiov1.Singleton {
		preFix = time.Now().String()
	}

	pvcName, err := RenderPVCName(preFix, config.Name, config.Namespace)
	if err != nil {
		logger.Error(err, "Unable to calculate hash")
		return nil, fmt.Errorf("unable to calculate hash: %w", err)
	}
	logger = logger.WithValues("pvc_name", pvcName)

	driver := drivers.GetDriver(provisioner)
	if driver == nil {
		logger.Info("Driver not found")
		return nil, errors.New("driver not found: " + provisioner)
	}

	pvc, err := driver.GetPVCStub(pvcName, config.Namespace, config.Spec.StorageClassName)
	if err != nil {
		logger.Error(err, "Unable to init a PVC", "provisioner", provisioner)
		return nil, fmt.Errorf("unable to init a PVC: %w", err)
	}

	if finalizer {
		pvc.Finalizers = []string{RenderFinalizer(config.Name)}
	}

	pvc.Labels = map[string]string{
		"discoblocks": config.Name,
	}

	capacity, err := resource.ParseQuantity(config.Spec.Capacity)
	if err != nil {
		logger.Error(err, "Capacity is invalid")
		return nil, fmt.Errorf("capacity is invalid [%s]: %w", config.Spec.Capacity, err)
	}

	pvc.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceStorage: capacity,
		},
	}

	pvc.Spec.AccessModes = config.Spec.AccessModes
	if len(pvc.Spec.AccessModes) == 0 {
		pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}

	return pvc, nil
}
