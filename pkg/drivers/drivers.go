package drivers

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/wasmerio/wasmer-go/wasmer"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
)

// DriversDir driver location, configure with -ldflags -X github.com/ondat/discoblocks/pkg/drivers.DriversDir=/yourpath
var DriversDir = "/drivers"

func init() {
	files, err := os.ReadDir(filepath.Clean(DriversDir))
	if err != nil {
		log.Fatal(fmt.Errorf("unable to load drivers: %w", err))
	}

	for _, file := range files {
		if !file.IsDir() {
			continue
		}

		driverPath := fmt.Sprintf("%s/%s/main.wasm", DriversDir, file.Name())

		if _, err := os.Stat(driverPath); err != nil {
			log.Printf("unable to find main.wasm for %s: %s", file.Name(), err.Error())
			continue
		}

		wasmBytes, err := os.ReadFile(filepath.Clean(driverPath))
		if err != nil {
			log.Fatal(fmt.Errorf("unable to load driver content for %s: %w", driverPath, err))
		}

		engine := wasmer.NewEngine()
		store := wasmer.NewStore(engine)
		module, err := wasmer.NewModule(store, wasmBytes)
		if err != nil {
			log.Fatal(fmt.Errorf("unable to compile module %s: %w", driverPath, err))
		}

		drivers[file.Name()] = &Driver{
			store:  store,
			module: module,
		}
	}
}

var drivers = map[string]*Driver{}

// GetDriver returns given service
func GetDriver(name string) *Driver {
	return drivers[name]
}

// Driver is the bridge to WASI modules
type Driver struct {
	store  *wasmer.Store
	module *wasmer.Module
}

// IsStorageClassValid validates StorageClass
func (d *Driver) IsStorageClassValid(sc *storagev1.StorageClass) (bool, error) {
	rawSc, err := json.Marshal(sc)
	if err != nil {
		return false, fmt.Errorf("unable to parse StorageClass: %w", err)
	}

	wasiEnv, instance, err := d.init(map[string]string{
		"STORAGE_CLASS_JSON": string(rawSc),
	})
	if err != nil {
		return false, fmt.Errorf("unable to init instance: %w", err)
	}

	isStorageClassValid, err := instance.Exports.GetRawFunction("IsStorageClassValid")
	if err != nil {
		return false, fmt.Errorf("unable to find IsStorageClassValid: %w", err)
	}

	_, err = isStorageClassValid.Native()()
	if err != nil {
		return false, fmt.Errorf("unable to call IsStorageClassValid: %w", err)
	}

	errOut := string(wasiEnv.ReadStderr())
	if errOut != "" {
		return false, fmt.Errorf("function error IsStorageClassValid: %s", errOut)
	}

	resp, err := strconv.ParseBool(string(wasiEnv.ReadStdout()))
	if err != nil {
		return false, fmt.Errorf("unable to parse output: %w", err)
	}

	return resp, nil
}

// GetStorageClassAllowedTopology validates StorageClass
func (d *Driver) GetStorageClassAllowedTopology(node *corev1.Node) ([]corev1.TopologySelectorTerm, error) {
	rawNode, err := json.Marshal(node)
	if err != nil {
		return nil, fmt.Errorf("unable to parse Node: %w", err)
	}

	wasiEnv, instance, err := d.init(map[string]string{
		"NODE_JSON": string(rawNode),
	})
	if err != nil {
		return nil, fmt.Errorf("unable to init instance: %w", err)
	}

	getStorageClassAllowedTopology, err := instance.Exports.GetRawFunction("GetStorageClassAllowedTopology")
	if err != nil {
		return nil, fmt.Errorf("unable to find GetStorageClassAllowedTopology: %w", err)
	}

	_, err = getStorageClassAllowedTopology.Native()()
	if err != nil {
		return nil, fmt.Errorf("unable to call GetStorageClassAllowedTopology: %w", err)
	}

	errOut := string(wasiEnv.ReadStderr())
	if errOut != "" {
		return nil, fmt.Errorf("function error GetStorageClassAllowedTopology: %s", errOut)
	}

	terms := []corev1.TopologySelectorTerm{}

	resp := wasiEnv.ReadStdout()
	if len(resp) != 0 {
		err = json.Unmarshal(wasiEnv.ReadStdout(), &terms)
		if err != nil {
			return nil, fmt.Errorf("unable to parse output: %w", err)
		}
	}

	return terms, nil
}

// GetPVCStub creates a PersistentVolumeClaim for driver
func (d *Driver) GetPVCStub(name, namespace, storageClassName string) (*corev1.PersistentVolumeClaim, error) {
	wasiEnv, instance, err := d.init(map[string]string{
		"PVC_NAME":           name,
		"PVC_NAMESACE":       namespace,
		"STORAGE_CLASS_NAME": storageClassName,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to init instance: %w", err)
	}

	getPVCStub, err := instance.Exports.GetRawFunction("GetPVCStub")
	if err != nil {
		return nil, fmt.Errorf("unable to find GetPVCStub: %w", err)
	}

	_, err = getPVCStub.Native()()
	if err != nil {
		return nil, fmt.Errorf("unable to call GetPVCStub: %w", err)
	}

	errOut := string(wasiEnv.ReadStderr())
	if errOut != "" {
		return nil, fmt.Errorf("function error GetPVCStub: %s", errOut)
	}

	pvc := corev1.PersistentVolumeClaim{}
	err = json.Unmarshal(wasiEnv.ReadStdout(), &pvc)
	if err != nil {
		return nil, fmt.Errorf("unable to parse output: %w", err)
	}

	return &pvc, nil
}

// GetCSIDriverDetails returns the labels of CSI driver Pod
func (d *Driver) GetCSIDriverDetails() (string, map[string]string, error) {
	wasiEnv, instance, err := d.init(nil)
	if err != nil {
		return "", nil, fmt.Errorf("unable to init instance: %w", err)
	}

	getCSIDriverNamespace, err := instance.Exports.GetRawFunction("GetCSIDriverNamespace")
	if err != nil {
		return "", nil, fmt.Errorf("unable to find GetCSIDriverNamespace: %w", err)
	}

	_, err = getCSIDriverNamespace.Native()()
	if err != nil {
		return "", nil, fmt.Errorf("unable to call GetCSIDriverNamespace: %w", err)
	}

	errOut := string(wasiEnv.ReadStderr())
	if errOut != "" {
		return "", nil, fmt.Errorf("function error GetCSIDriverNamespace: %s", errOut)
	}

	namespace := wasiEnv.ReadStdout()

	getCSIDriverPodLabels, err := instance.Exports.GetRawFunction("GetCSIDriverPodLabels")
	if err != nil {
		return "", nil, fmt.Errorf("unable to find GetCSIDriverPodLabels: %w", err)
	}

	_, err = getCSIDriverPodLabels.Native()()
	if err != nil {
		return "", nil, fmt.Errorf("unable to call GetCSIDriverPodLabels: %w", err)
	}

	errOut = string(wasiEnv.ReadStderr())
	if errOut != "" {
		return "", nil, fmt.Errorf("function error GetCSIDriverPodLabels: %s", errOut)
	}

	labels := map[string]string{}
	err = json.Unmarshal(wasiEnv.ReadStdout(), &labels)
	if err != nil {
		return "", nil, fmt.Errorf("unable to parse output GetCSIDriverPodLabels: %w", err)
	}

	return string(namespace), labels, nil
}

// GetPreMountCommand returns pre mount command
func (d *Driver) GetPreMountCommand(pv *corev1.PersistentVolume, va *storagev1.VolumeAttachment) (string, error) {
	rawPV, err := json.Marshal(pv)
	if err != nil {
		return "", fmt.Errorf("unable to parse PersistentVolume: %w", err)
	}

	rawVA, err := json.Marshal(va)
	if err != nil {
		return "", fmt.Errorf("unable to parse VolumeAttachment: %w", err)
	}

	wasiEnv, instance, err := d.init(map[string]string{
		"PERSISTENT_VOLUME_JSON": string(rawPV),
		"VOLUME_ATTACHMENT_JSON": string(rawVA),
	})
	if err != nil {
		return "", fmt.Errorf("unable to init instance: %w", err)
	}

	getPreMountCommand, err := instance.Exports.GetRawFunction("GetPreMountCommand")
	if err != nil {
		return "", fmt.Errorf("unable to find GetPreMountCommand: %w", err)
	}

	_, err = getPreMountCommand.Native()()
	if err != nil {
		return "", fmt.Errorf("unable to call GetPreMountCommand: %w", err)
	}

	errOut := string(wasiEnv.ReadStderr())
	if errOut != "" {
		return "", fmt.Errorf("function error GetPreMountCommand: %s", errOut)
	}

	return string(wasiEnv.ReadStdout()), nil
}

// GetPreResizeCommand returns pre resize command
func (d *Driver) GetPreResizeCommand(pv *corev1.PersistentVolume, va *storagev1.VolumeAttachment) (string, error) {
	rawPV, err := json.Marshal(pv)
	if err != nil {
		return "", fmt.Errorf("unable to parse PersistentVolume: %w", err)
	}

	rawVA := []byte{}
	if va != nil {
		rawVA, err = json.Marshal(va)
		if err != nil {
			return "", fmt.Errorf("unable to parse VolumeAttachment: %w", err)
		}
	}

	wasiEnv, instance, err := d.init(map[string]string{
		"PERSISTENT_VOLUME_JSON": string(rawPV),
		"VOLUME_ATTACHMENT_JSON": string(rawVA),
	})
	if err != nil {
		return "", fmt.Errorf("unable to init instance: %w", err)
	}

	getPreResizeCommand, err := instance.Exports.GetRawFunction("GetPreResizeCommand")
	if err != nil {
		return "", fmt.Errorf("unable to find GetPreResizeCommand: %w", err)
	}

	_, err = getPreResizeCommand.Native()()
	if err != nil {
		return "", fmt.Errorf("unable to call GetPreResizeCommand: %w", err)
	}

	errOut := string(wasiEnv.ReadStderr())
	if errOut != "" {
		return "", fmt.Errorf("function error GetPreResizeCommand: %s", errOut)
	}

	return string(wasiEnv.ReadStdout()), nil
}

// IsFileSystemManaged determines is file system managed by driver
func (d *Driver) IsFileSystemManaged() (bool, error) {
	wasiEnv, instance, err := d.init(nil)
	if err != nil {
		return false, fmt.Errorf("unable to init instance: %w", err)
	}

	isFileSystemManaged, err := instance.Exports.GetRawFunction("IsFileSystemManaged")
	if err != nil {
		return false, fmt.Errorf("unable to find IsFileSystemManaged: %w", err)
	}

	_, err = isFileSystemManaged.Native()()
	if err != nil {
		return false, fmt.Errorf("unable to call IsFileSystemManaged: %w", err)
	}

	errOut := string(wasiEnv.ReadStderr())
	if errOut != "" {
		return false, fmt.Errorf("function error IsFileSystemManaged: %s", errOut)
	}

	resp, err := strconv.ParseBool(string(wasiEnv.ReadStdout()))
	if err != nil {
		return false, fmt.Errorf("unable to parse output: %w", err)
	}

	return resp, nil
}

// WaitForVolumeAttachmentMeta defines wait for device info of plugin
func (d *Driver) WaitForVolumeAttachmentMeta() (string, error) {
	wasiEnv, instance, err := d.init(nil)
	if err != nil {
		return "", fmt.Errorf("unable to init instance: %w", err)
	}

	waitCommand, err := instance.Exports.GetRawFunction("WaitForVolumeAttachmentMeta")
	if err != nil {
		return "", fmt.Errorf("unable to find WaitForVolumeAttachmentMeta: %w", err)
	}

	_, err = waitCommand.Native()()
	if err != nil {
		return "", fmt.Errorf("unable to call WaitForVolumeAttachmentMeta: %w", err)
	}

	errOut := string(wasiEnv.ReadStderr())
	if errOut != "" {
		return "", fmt.Errorf("function error WaitForVolumeAttachmentMeta: %s", errOut)
	}

	return string(wasiEnv.ReadStdout()), nil
}

func (d *Driver) init(envs map[string]string) (*wasmer.WasiEnvironment, *wasmer.Instance, error) {
	builder := wasmer.NewWasiStateBuilder("wasi-program").
		CaptureStdout().CaptureStderr()

	for k, v := range envs {
		builder = builder.Environment(k, v)
	}

	wasiEnv, err := builder.Finalize()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to build module: %w", err)
	}

	importObject, err := wasiEnv.GenerateImportObject(d.store, d.module)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to generate imports: %w", err)
	}

	instance, err := wasmer.NewInstance(d.module, importObject)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to create instance: %w", err)
	}

	start, err := instance.Exports.GetWasiStartFunction()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to get start: %w", err)
	}

	_, err = start()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to start instance: %w", err)
	}

	return wasiEnv, instance, nil
}
