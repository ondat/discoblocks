package main

import (
	"fmt"
	"os"

	"github.com/valyala/fastjson"
)

func main() {}

//export IsStorageClassValid
func IsStorageClassValid() {
	json := []byte(os.Getenv("STORAGE_CLASS_JSON"))

	if !fastjson.Exists(json, "allowVolumeExpansion") || !fastjson.GetBool(json, "allowVolumeExpansion") {
		fmt.Fprint(os.Stderr, "only allowVolumeExpansion true is supported")
		fmt.Fprint(os.Stdout, false)
		return
	}

	fmt.Fprint(os.Stdout, true)
}

//export GetPVCStub
func GetPVCStub() {
	fmt.Fprintf(os.Stdout, `{
	"apiVersion": "v1",
	"kind": "PersistentVolumeClaim",
	"metadata": {
		"name": "%s",
		"namespace": "%s"
	},
	"spec": {
		"storageClassName": "%s"
	}
}`,
		os.Getenv("PVC_NAME"), os.Getenv("PVC_NAMESACE"), os.Getenv("STORAGE_CLASS_NAME"))
}

//export GetCSIDriverNamespace
func GetCSIDriverNamespace() {
	fmt.Fprint(os.Stdout, "storageos")
}

//export GetCSIDriverPodLabels
func GetCSIDriverPodLabels() {
	fmt.Fprint(os.Stdout, `{ "app": "storageos", "app.kubernetes.io/component": "csi" }`)
}

//export GetDevicePath
func GetDevicePath() {
	fmt.Fprint(os.Stdout, "/var/lib/storageos/volumes")
}

//export GetDeviceLookupCommand
func GetDeviceLookupCommand() {
	fmt.Fprint(os.Stdout, `ls ${DEV_PATH} -Atr | tail -1`)
}

//export IsFileSystemManaged
func IsFileSystemManaged() {
	fmt.Fprint(os.Stdout, true)
}

//export WaitForVolumeAttachmentMeta
func WaitForVolumeAttachmentMeta() {}
