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

//export GetMountCommand
func GetMountCommand() {
	fmt.Fprint(os.Stdout, `DEV=$(chroot /host ls /var/lib/storageos/volumes/ -Atr | tail -1) &&
chroot /host nsenter --target 1 --mount mkdir -p /var/lib/kubelet/discoblocks/${MOUNT_ID}${MOUNT_POINT} &&
chroot /host nsenter --target 1 --mount mount /var/lib/storageos/volumes/${DEV} /var/lib/kubelet/discoblocks/${MOUNT_ID}${MOUNT_POINT} &&
DEV_MAJOR=$(chroot /host nsenter --target 1 --mount cat /proc/self/mountinfo | grep ${DEV} | awk '{print $3}'  | awk '{split($0,a,":"); print a[1]}') &&
DEV_MINOR=$(chroot /host nsenter --target 1 --mount cat /proc/self/mountinfo | grep ${DEV} | awk '{print $3}'  | awk '{split($0,a,":"); print a[2]}') &&
for CONTAINER_ID in ${CONTAINER_IDS}; do
	PID=$(crictl inspect --output go-template --template '{{.info.pid}}' ${CONTAINER_ID}) &&
	chroot /host nsenter --target ${PID} --mount mkdir -p ${MOUNT_POINT} /tmp${MOUNT_POINT} &&
	chroot /host nsenter --target ${PID} --mount mknod --mode 0600 /tmp${MOUNT_POINT}/mount b ${DEV_MAJOR} ${DEV_MINOR} &&
	chroot /host nsenter --target ${PID} --mount mount /tmp${MOUNT_POINT}/mount ${MOUNT_POINT}
done`)
}
