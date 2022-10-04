package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRenderMetricsService(t *testing.T) {
	_, err := RenderMetricsService("name", "namespace")

	assert.Nil(t, err, "invalid metrics service template")
}

func TestRenderMetricsSidecar(t *testing.T) {
	_, err := RenderMetricsSidecar(true)

	assert.Nil(t, err, "invalid sidecar template")
}

func TestRenderAttachJob(t *testing.T) {
	_, err := RenderAttachJob("name", "namespace", "nodename", metav1.OwnerReference{})

	assert.Nil(t, err, "invalid attach job")
}

func TestRenderMountJob(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		fsManaged       bool
		hostPID         bool
		expectedCommand []string
	}{
		"FS not managed, not host PID": {
			expectedCommand: []string{
				"bash",
				"-exc",
				"sleep infinity ; DEV=$(chroot /host nsenter --target 1 --mount lookupCommand) &&\nchroot /host nsenter --target 1 --mount mkfs.${FS} ${DEV_PATH}/${DEV} &&\nchroot /host nsenter --target 1 --mount mkdir -p /var/lib/kubelet/plugins/kubernetes.io/csi/pv/${PVC_NAME} &&\nchroot /host nsenter --target 1 --mount mount ${DEV_PATH}/${DEV} /var/lib/kubelet/plugins/kubernetes.io/csi/pv/${PVC_NAME} &&\nDEV_MAJOR=$(chroot /host nsenter --target 1 --mount cat /proc/self/mountinfo | grep ${DEV} | awk '{print $3}'  | awk '{split($0,a,\":\"); print a[1]}') &&\nDEV_MINOR=$(chroot /host nsenter --target 1 --mount cat /proc/self/mountinfo | grep ${DEV} | awk '{print $3}'  | awk '{split($0,a,\":\"); print a[2]}') &&\nfor CONTAINER_ID in ${CONTAINER_IDS}; do\n\tPID=$(docker inspect -f '{{.State.Pid}}' ${CONTAINER_ID} || crictl inspect --output go-template --template '{{.info.pid}}' ${CONTAINER_ID}) &&\n\tchroot /host nsenter --target ${PID} --mount mkdir -p /dev ${MOUNT_POINT} &&\n\tchroot /host nsenter --target ${PID} --pid --mount mknod /dev/${DEV} b ${DEV_MAJOR} ${DEV_MINOR} &&\n\tchroot /host nsenter --target ${PID} --mount mount /dev/${DEV} ${MOUNT_POINT}\ndone &&\necho ok\n",
			},
		},
		"FS managed, not host PID": {
			fsManaged: true,
			expectedCommand: []string{
				"bash",
				"-exc",
				"sleep infinity ; DEV=$(chroot /host nsenter --target 1 --mount lookupCommand) &&\n\nchroot /host nsenter --target 1 --mount mkdir -p /var/lib/kubelet/plugins/kubernetes.io/csi/pv/${PVC_NAME} &&\nchroot /host nsenter --target 1 --mount mount ${DEV_PATH}/${DEV} /var/lib/kubelet/plugins/kubernetes.io/csi/pv/${PVC_NAME} &&\nDEV_MAJOR=$(chroot /host nsenter --target 1 --mount cat /proc/self/mountinfo | grep ${DEV} | awk '{print $3}'  | awk '{split($0,a,\":\"); print a[1]}') &&\nDEV_MINOR=$(chroot /host nsenter --target 1 --mount cat /proc/self/mountinfo | grep ${DEV} | awk '{print $3}'  | awk '{split($0,a,\":\"); print a[2]}') &&\nfor CONTAINER_ID in ${CONTAINER_IDS}; do\n\tPID=$(docker inspect -f '{{.State.Pid}}' ${CONTAINER_ID} || crictl inspect --output go-template --template '{{.info.pid}}' ${CONTAINER_ID}) &&\n\tchroot /host nsenter --target ${PID} --mount mkdir -p /dev ${MOUNT_POINT} &&\n\tchroot /host nsenter --target ${PID} --pid --mount mknod /dev/${DEV} b ${DEV_MAJOR} ${DEV_MINOR} &&\n\tchroot /host nsenter --target ${PID} --mount mount /dev/${DEV} ${MOUNT_POINT}\ndone &&\necho ok\n",
			},
		},
		"FS not managed, host PID": {
			hostPID: true,
			expectedCommand: []string{
				"bash",
				"-exc",
				"sleep infinity ; DEV=$(chroot /host nsenter --target 1 --mount lookupCommand) &&\nchroot /host nsenter --target 1 --mount mkfs.${FS} ${DEV_PATH}/${DEV} &&\nchroot /host nsenter --target 1 --mount mkdir -p /mountPoint &&\nchroot /host nsenter --target 1 --mount mount ${DEV_PATH}/${DEV} /mountPoint &&\n\necho ok\n",
			},
		},
	}

	for n, c := range cases {
		c := c
		t.Run(n, func(t *testing.T) {
			t.Parallel()

			job, err := RenderMountJob("name", "namespace", "nodeName", "dev", "/devPath", "fs", "/mountPoint", []string{"c1", "c2"}, "lookupCommand", c.fsManaged, c.hostPID, metav1.OwnerReference{})
			assert.Nil(t, err, "invalid mount job template")

			assert.Equal(t, c.expectedCommand, job.Spec.Template.Spec.Containers[0].Command, "invalid mount command")
		})
	}
}
