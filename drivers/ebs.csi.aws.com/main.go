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

//export GetMountCommand
func GetMountCommand() {
	fmt.Fprint(os.Stdout, `DEV=$(chroot /host nsenter --target 1 --mount readlink -f ${DEV} | sed "s|.*/||") &&
chroot /host nsenter --target 1 --mount mkfs.${FS} /dev/${DEV} &&
chroot /host nsenter --target 1 --mount mkdir -p /var/lib/kubelet/plugins/kubernetes.io/csi/pv/${PVC_NAME} &&
chroot /host nsenter --target 1 --mount mount /dev/${DEV} /var/lib/kubelet/plugins/kubernetes.io/csi/pv/${PVC_NAME} &&
DEV_MAJOR=$(chroot /host nsenter --target 1 --mount cat /proc/self/mountinfo | grep ${DEV} | awk '{print $3}'  | awk '{split($0,a,":"); print a[1]}') &&
DEV_MINOR=$(chroot /host nsenter --target 1 --mount cat /proc/self/mountinfo | grep ${DEV} | awk '{print $3}'  | awk '{split($0,a,":"); print a[2]}') &&
for CONTAINER_ID in ${CONTAINER_IDS}; do
	PID=$(docker inspect -f '{{.State.Pid}}' ${CONTAINER_ID} || crictl inspect --output go-template --template '{{.info.pid}}' ${CONTAINER_ID}) &&
	chroot /host nsenter --target ${PID} --mount mkdir -p /dev ${MOUNT_POINT} &&
	chroot /host nsenter --target ${PID} --pid --mount mknod /dev/${DEV} b ${DEV_MAJOR} ${DEV_MINOR} &&
	chroot /host nsenter --target ${PID} --mount mount /dev/${DEV} ${MOUNT_POINT}
done`)
}

//export GetResizeCommand
func GetResizeCommand() {
	fmt.Fprint(os.Stdout, `DEV=$(chroot /host nsenter --target 1 --mount readlink -f ${DEV}) &&
(
	([ "${FS}" = "ext3" ] && chroot /host nsenter --target 1 --mount resize2fs ${DEV}) ||
	([ "${FS}" = "ext4" ] && chroot /host nsenter --target 1 --mount resize2fs ${DEV}) ||
	([ "${FS}" = "xfs" ] && chroot /host nsenter --target 1 --mount xfs_growfs -d ${DEV}) ||
	([ "${FS}" = "btrfs" ] && chroot /host nsenter --target 1 --mount btrfs filesystem resize max ${DEV}) ||
	echo unsupported file-system $FS
)
	`)
	fmt.Fprint(os.Stdout, `DEV=$(chroot /host nsenter --target 1 --mount readlink -f ${DEV}) && chroot /host nsenter --target 1 --mount resize2fs ${DEV}`)
}

//export WaitForVolumeAttachmentMeta
func WaitForVolumeAttachmentMeta() {
	fmt.Fprint(os.Stdout, "devicePath")
}
