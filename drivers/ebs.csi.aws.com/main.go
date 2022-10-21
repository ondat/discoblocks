package main

import (
	"fmt"
	"os"
	"strings"

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

//export GetStorageClassAllowedTopology
func GetStorageClassAllowedTopology() {
	json := []byte(os.Getenv("NODE_JSON"))

	zone := fastjson.GetString(json, "metadata", "labels", "topology.kubernetes.io/zone")
	if zone == "" {
		fmt.Fprint(os.Stderr, "metadata.labels.'topology.kubernetes.io/zone' not found")
		return
	}

	fmt.Fprintf(os.Stdout, `[{
	"matchLabelExpressions": [
		{
			"key": "topology.kubernetes.io/zone",
			"values": [ "%s" ]
		}
	]
}]`, zone)
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
	fmt.Fprint(os.Stdout, "kube-system")
}

//export GetCSIDriverPodLabels
func GetCSIDriverPodLabels() {
	fmt.Fprint(os.Stdout, `{ "app": "ebs-csi-controller" }`)
}

//export GetPreMountCommand
func GetPreMountCommand() {
	json := []byte(os.Getenv("PERSISTENT_VOLUME_JSON"))

	volumeHandle := strings.ReplaceAll(fastjson.GetString(json, "spec", "csi", "volumeHandle"), "-", "")
	if volumeHandle == "" {
		fmt.Fprint(os.Stderr, "spec.csi.volumeHandle not found")
		return
	}

	fmt.Fprintf(os.Stdout, `DEV=$(nvme list | grep %s | awk '{print $1}') &&
(chroot /host nsenter --target 1 --mount mkfs.${FS} ${DEV} ||:)`,
		volumeHandle)
}

//export GetPreResizeCommand
func GetPreResizeCommand() {
	json := []byte(os.Getenv("PERSISTENT_VOLUME_JSON"))

	volumeHandle := strings.ReplaceAll(fastjson.GetString(json, "spec", "csi", "volumeHandle"), "-", "")
	if volumeHandle == "" {
		fmt.Fprint(os.Stderr, "spec.csi.volumeHandle not found")
		return
	}

	fmt.Fprintf(os.Stdout, `DEV=$(nvme list | grep %s | awk '{print $1}') &&
chroot /host nsenter --target 1 --mount mkdir -p /tmp/discoblocks${DEV} &&
chroot /host nsenter --target 1 --mount mount ${DEV} /tmp/discoblocks${DEV} &&
trap "chroot /host nsenter --target 1 --mount umount /tmp/discoblocks${DEV}" EXIT`,
		volumeHandle)
}

//export IsFileSystemManaged
func IsFileSystemManaged() {
	fmt.Fprint(os.Stdout, false)
}

//export WaitForVolumeAttachmentMeta
func WaitForVolumeAttachmentMeta() {}
