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
			log.Printf("unable to found main.wasm for %s: %s", file.Name(), err.Error())
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
	if errOut != "" {
		return false, fmt.Errorf("unable to parse output: %w", err)
	}

	return resp, nil
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
	if errOut != "" {
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
	if errOut != "" {
		return "", nil, fmt.Errorf("unable to parse output GetCSIDriverPodLabels: %w", err)
	}

	return string(namespace), labels, nil
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
