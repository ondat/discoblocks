package drivers

import (
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
)

var drivers = map[string]Driver{}

// GetDriver returns given service
func GetDriver(name string) Driver {
	return drivers[name]
}

// SetDriver sets driver by name
func SetDriver(name string, driver Driver) {
	drivers[name] = driver
}

// Driver public functionality of a driver
type Driver interface {
	IsStorageClassValid(*storagev1.StorageClass) error
	GetPVCStub(string, string, string) (*corev1.PersistentVolumeClaim, error)
}
