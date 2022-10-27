package utils

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/drivers"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// Used for Yaml indentation
const hostCommandPrefix = "\n          "

var hostCommandReplacePattern = regexp.MustCompile(`\n`)

const metricsTeamplate = `name: discoblocks-metrics
image: nixery.dev/shell/ucspi-tcp/mount
ports:
- containerPort: 9100
  protocol: TCP
command:
- sh
- -c
- |
  trap exit SIGTERM ;
  while true; do tcpserver -v -c 1 -D -P -R -H -t 3 -l 0 0.0.0.0 9100 df -P & c=$! wait $c; done
securityContext:
  privileged: false
`

const hostJobTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: "%s"
  namespace: "%s"
  labels:
    app: discoblocks
spec:
  template:
    spec:
      hostPID: true
      nodeName: "%s"
      containers:
      - name: mount
        image: nixery.dev/shell/gawk/gnugrep/gnused/coreutils-full/cri-tools/docker-client/nvme-cli
        env:
        - name: MOUNT_POINT
          value: "%s"
        - name: CONTAINER_IDS
          value: "%s"
        - name: PVC_NAME
          value: "%s"
        - name: PV_NAME
          value: "%s"
        - name: FS
          value: "%s"
        - name: VOLUME_ATTACHMENT_META
          value: "%s"
        command:
        - bash
        - -exc
        - |
          %s
        volumeMounts:
        - mountPath: /run/containerd/containerd.sock
          name: containerd-socket
          readOnly: true
        - mountPath: /var/run/docker.sock
          name: docker-socket
          readOnly: true
        - mountPath: /host
          name: host
        securityContext:
          privileged: true
      restartPolicy: Never
      volumes:
       - hostPath:
          path: /run/containerd/containerd.sock
         name: containerd-socket
       - hostPath:
          path: /var/run/docker.sock
         name: docker-socket
       - hostPath:
          path: /
         name: host
  backoffLimit: 0
  ttlSecondsAfterFinished: 86400
`

const (
	mountCommandTemplate = `%s
DEV_MAJOR=$(chroot /host nsenter --target 1 --mount lsblk -lp | grep ${DEV} | awk '{print $2}'  | awk '{split($0,a,":"); print a[1]}') &&
DEV_MINOR=$(chroot /host nsenter --target 1 --mount lsblk -lp | grep ${DEV} | awk '{print $2}'  | awk '{split($0,a,":"); print a[2]}') &&
for CONTAINER_ID in ${CONTAINER_IDS}; do
	chroot /host nsenter --target ${PID} --mount mount | grep "${DEV} on ${MOUNT_POINT}" || (
		PID=$(docker inspect -f '{{.State.Pid}}' ${CONTAINER_ID} || crictl inspect --output go-template --template '{{.info.pid}}' ${CONTAINER_ID}) &&
		(
			chroot /host nsenter --target ${PID} --mount mkdir -p $(dirname ${DEV}) ${MOUNT_POINT} &&
			chroot /host nsenter --target ${PID} --pid --mount mknod ${DEV} b ${DEV_MAJOR} ${DEV_MINOR} &&
			chroot /host nsenter --target ${PID} --mount mount ${DEV} ${MOUNT_POINT}
		)
	)
done`
)

const resizeCommandTemplate = `%s
chroot /host nsenter --target 1 --mount mkdir -p /tmp/discoblocks${DEV} &&
chroot /host nsenter --target 1 --mount mount ${DEV} /tmp/discoblocks${DEV} &&
trap "chroot /host nsenter --target 1 --mount umount /tmp/discoblocks${DEV}" EXIT &&
(
	([ "${FS}" = "ext3" ] && chroot /host nsenter --target 1 --mount resize2fs ${DEV}) ||
	([ "${FS}" = "ext4" ] && chroot /host nsenter --target 1 --mount resize2fs ${DEV}) ||
	([ "${FS}" = "xfs" ] && chroot /host nsenter --target 1 --mount xfs_growfs -d ${DEV}) ||
	([ "${FS}" = "btrfs" ] && chroot /host nsenter --target 1 --mount btrfs filesystem resize max ${DEV}) ||
	echo unsupported file-system $FS
)`

// RenderMetricsSidecar returns the metrics sidecar
func RenderMetricsSidecar() (*corev1.Container, error) {
	sidecar := corev1.Container{}
	if err := yaml.Unmarshal([]byte(metricsTeamplate), &sidecar); err != nil {
		return nil, fmt.Errorf("unable to unmarshal container: %w", err)
	}

	return &sidecar, nil
}

// RenderMountJob returns the mount job executed on host
func RenderMountJob(pvcName, pvName, namespace, nodeName, fs, mountPoint string, containerIDs []string, preMountCommand, volumeMeta string, owner metav1.OwnerReference) (*batchv1.Job, error) {
	if preMountCommand != "" {
		preMountCommand += " && "
	}

	mountCommand := fmt.Sprintf(mountCommandTemplate, preMountCommand)
	mountCommand = string(hostCommandReplacePattern.ReplaceAll([]byte(mountCommand), []byte(hostCommandPrefix)))

	jobName, err := RenderResourceName(true, fmt.Sprintf("%d", time.Now().UnixNano()), pvcName, namespace)
	if err != nil {
		return nil, fmt.Errorf("unable to render resource name: %w", err)
	}

	template := fmt.Sprintf(hostJobTemplate, jobName, namespace, nodeName, mountPoint, strings.Join(containerIDs, " "), pvcName, pvName, fs, volumeMeta, mountCommand)

	job := batchv1.Job{}
	if err := yaml.Unmarshal([]byte(template), &job); err != nil {
		println(template)
		return nil, fmt.Errorf("unable to unmarshal job: %w", err)
	}

	job.OwnerReferences = []metav1.OwnerReference{
		owner,
	}

	return &job, nil
}

// RenderResizeJob returns the resize job executed on host
func RenderResizeJob(pvcName, pvName, namespace, nodeName, fs, preResizeCommand, volumeMeta string, owner metav1.OwnerReference) (*batchv1.Job, error) {
	if preResizeCommand != "" {
		preResizeCommand += " && "
	}

	resizeCommand := fmt.Sprintf(resizeCommandTemplate, preResizeCommand)
	resizeCommand = string(hostCommandReplacePattern.ReplaceAll([]byte(resizeCommand), []byte(hostCommandPrefix)))

	jobName, err := RenderResourceName(true, fmt.Sprintf("%d", time.Now().UnixNano()), pvcName, namespace)
	if err != nil {
		return nil, fmt.Errorf("unable to render resource name: %w", err)
	}

	template := fmt.Sprintf(hostJobTemplate, jobName, namespace, nodeName, "", "", pvcName, pvName, fs, volumeMeta, resizeCommand)

	job := batchv1.Job{}
	if err := yaml.Unmarshal([]byte(template), &job); err != nil {
		println(template)
		return nil, fmt.Errorf("unable to unmarshal job: %w", err)
	}

	job.OwnerReferences = []metav1.OwnerReference{
		owner,
	}

	return &job, nil
}

// NewPVC constructs a new PVC instance
func NewPVC(config *discoblocksondatiov1.DiskConfig, prefix string, driver *drivers.Driver) (*corev1.PersistentVolumeClaim, error) {
	pvcName, err := RenderResourceName(true, prefix, config.Name, config.Namespace)
	if err != nil {
		return nil, fmt.Errorf("unable to calculate hash: %w", err)
	}

	pvc, err := driver.GetPVCStub(pvcName, config.Namespace, config.Spec.StorageClassName)
	if err != nil {
		return nil, fmt.Errorf("unable to init a PVC: %w", err)
	}

	pvc.Finalizers = []string{RenderFinalizer(config.Name)}

	pvc.Labels = map[string]string{
		"discoblocks": config.Name,
	}

	pvc.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceStorage: config.Spec.Capacity,
		},
	}

	pvc.Spec.AccessModes = config.Spec.AccessModes
	if len(pvc.Spec.AccessModes) == 0 {
		pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}

	return pvc, nil
}

// NewStorageClass constructs a new StorageClass
func NewStorageClass(sc *storagev1.StorageClass, scAllowedTopology []corev1.TopologySelectorTerm) (*storagev1.StorageClass, error) {
	topologyItems := ""
	for _, ti := range scAllowedTopology {
		topologyItems += ti.String()
	}

	tmpScName, err := RenderResourceName(true, string(sc.UID), sc.Name, topologyItems)
	if err != nil {
		return nil, fmt.Errorf("failed to render RenderResourceName of tmp StorageClass: %w", err)
	}

	topologySC := sc.DeepCopy()
	topologySC.UID = ""
	topologySC.ResourceVersion = ""
	topologySC.Name = tmpScName
	bm := storagev1.VolumeBindingImmediate
	topologySC.VolumeBindingMode = &bm
	topologySC.AllowedTopologies = scAllowedTopology

	return topologySC, nil
}

// IsOwnedByDaemonSet detects is parent DaemonSet
func IsOwnedByDaemonSet(pod *corev1.Pod) bool {
	for i := range pod.OwnerReferences {
		if pod.OwnerReferences[i].Kind == "DaemonSet" && pod.OwnerReferences[i].APIVersion == appsv1.SchemeGroupVersion.String() {
			return true
		}
	}

	return false
}

// GetTargetNodeByAffinity tries to find node by affinity
func GetTargetNodeByAffinity(affinit *corev1.Affinity) string {
	if affinit == nil ||
		affinit.NodeAffinity == nil ||
		affinit.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return ""
	}

	for _, term := range affinit.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		for _, mf := range term.MatchFields {
			if mf.Key == "metadata.name" && mf.Operator == corev1.NodeSelectorOpIn && len(mf.Values) > 0 {
				// DeamonSet controller sets only one: https://sourcegraph.com/github.com/kubernetes/kubernetes@edd677694374fb8284b9ddd04caf0698eaf00de5/-/blob/pkg/controller/daemon/util/daemonset_util.go?L216
				return mf.Values[0]
			}
		}
	}

	return ""
}
