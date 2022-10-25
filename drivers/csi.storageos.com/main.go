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

	if fastjson.Exists(json, "volumeBindingMode") && fastjson.GetString(json, "volumeBindingMode") != "Immediate" {
		fmt.Fprint(os.Stderr, "only volumeBindingMode Immediate is supported")
		fmt.Fprint(os.Stdout, false)
		return
	}

	if !fastjson.Exists(json, "allowVolumeExpansion") || !fastjson.GetBool(json, "allowVolumeExpansion") {
		fmt.Fprint(os.Stderr, "only allowVolumeExpansion true is supported")
		fmt.Fprint(os.Stdout, false)
		return
	}

	fmt.Fprint(os.Stdout, true)
}

//export GetStorageClassAllowedTopology
func GetStorageClassAllowedTopology() {}

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

//export GetPreMountCommand
func GetPreMountCommand() {
	fmt.Fprintf(os.Stdout, `VOL=$(chroot /host nsenter --target 1 --mount sh -c "grep ^ /dev/null /var/lib/storageos/state/*" | grep ${PV_NAME} | awk '{split($0,a,":"); print a[1]}' | grep -oe "v\..*\.json$"| awk '{gsub(".json","",$1); print $1}') &&
chroot /host nsenter --target 1 --mount mkdir -p /var/lib/kubelet/plugins/kubernetes.io/csi/pv/${PV_NAME}/mount &&
chroot /host nsenter --target 1 --mount mount /var/lib/storageos/volumes/${VOL} /var/lib/kubelet/plugins/kubernetes.io/csi/pv/${PV_NAME}/mount &&
DEV=/${PV_NAME}`)
}

//export GetPreResizeCommand
func GetPreResizeCommand() {}

//export IsFileSystemManaged
func IsFileSystemManaged() {
	fmt.Fprint(os.Stdout, true)
}

//export WaitForVolumeAttachmentMeta
func WaitForVolumeAttachmentMeta() {}
