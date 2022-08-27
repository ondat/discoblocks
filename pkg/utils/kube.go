package utils

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/drivers"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"
)

// TODO on this way on case of multiple discoblocks on a pod,
// all service would capture all disks leads to redundant data
const metricsServiceTemplate = `kind: Service
apiVersion: v1
metadata:
  name: "%s"
  namespace: "%s"
  annotations:
    prometheus.io/path: "/metrics"
    prometheus.io/scrape: "true"
    prometheus.io/port:   "9100"
spec:
  ports:
  - name: node-exporter
    protocol: TCP
    port: 9100
    targetPort: 9100
`

// TODO limit filesystem reports to discoblocks (ignored-mount-points)
const metricsTeamplate = `name: discoblocks-metrics
image: bitnami/node-exporter:1.3.1
ports:
- containerPort: 9100
  protocol: TCP
command:
- /opt/bitnami/node-exporter/bin/node_exporter
- --collector.disable-defaults
- --collector.filesystem
`

// XXX replace nixery image
const mountJobTemplate = `apiVersion: batch/v1
kind: Job
metadata:
  name: "%s"
  namespace: "%s"
spec:
  template:
    spec:
      containers:
      - name: mount
        image: nixery.dev/shell/gawk/gnugrep/mount/util-linux/coreutils-full/cri-tools
        env:
        - name: MOUNT_ID
          value: "%s"
        - name: MOUNT_POINT
          value: "%s"
        - name: CONTAINER_IDS
          value: "%s"
        command:
        - bash
        - -exc
        - |
          DEV=$(ls /host/var/lib/storageos/volumes/ -Atr | tail -1) &&
          mkdir -p /host/var/lib/kubelet/discoblocks/${MOUNT_ID}${MOUNT_POINT} &&
          chroot /host mount /var/lib/storageos/volumes/${DEV} /var/lib/kubelet/discoblocks/${MOUNT_ID}${MOUNT_POINT} &&
          DEV_MAJOR=$(cat /host/proc/self/mountinfo | grep ${DEV} | awk '{print $3}'  | awk '{split($0,a,":"); print a[1]}') &&
          DEV_MINOR=$(cat /host/proc/self/mountinfo | grep ${DEV} | awk '{print $3}'  | awk '{split($0,a,":"); print a[2]}') &&
          for CONTAINER_ID in ${CONTAINER_IDS}; do
            PID=$(crictl inspect --output go-template --template '{{.info.pid}}' ${CONTAINER_ID}) &&
            chroot /host nsenter --target ${PID} --mount mkdir -p ${MOUNT_POINT} /tmp${MOUNT_POINT} &&
            chroot /host nsenter --target ${PID} --mount mknod --mode 0600 /tmp${MOUNT_POINT}/mount b ${DEV_MAJOR} ${DEV_MINOR} &&
            chroot /host nsenter --target ${PID} --mount mount /tmp${MOUNT_POINT}/mount ${MOUNT_POINT}
          done
        volumeMounts:
        - mountPath: /run/containerd/containerd.sock
          name: containerd-socket
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
          path: /
         name: host
  backoffLimit: 10
`

// RenderMetricsService returns the metrics service
func RenderMetricsService(name, namespace string) (*corev1.Service, error) {
	service := corev1.Service{}
	if err := yaml.Unmarshal([]byte(fmt.Sprintf(metricsServiceTemplate, name, namespace)), &service); err != nil {
		return nil, err
	}

	return &service, nil
}

// RenderMetricsSidecar returns the metrics sidecar
func RenderMetricsSidecar() (*corev1.Container, error) {
	sidecar := corev1.Container{}
	if err := yaml.Unmarshal([]byte(metricsTeamplate), &sidecar); err != nil {
		return nil, err
	}

	return &sidecar, nil
}

// RenderMountJob returns the mount job
func RenderMountJob(name, namespace, mountID, mountPoint string, containerIDs []string) (*batchv1.Job, error) {
	job := batchv1.Job{}
	if err := yaml.Unmarshal([]byte(fmt.Sprintf(mountJobTemplate, name, namespace, mountID, mountPoint, strings.Join(containerIDs, " "))), &job); err != nil {
		return nil, err
	}

	return &job, nil
}

// NewPVC constructs a new PVC instance
func NewPVC(config *discoblocksondatiov1.DiskConfig, provisioner string, logger logr.Logger) (*corev1.PersistentVolumeClaim, error) {
	preFix := config.CreationTimestamp.String()
	if config.Spec.AvailabilityMode != discoblocksondatiov1.Singleton {
		preFix = time.Now().String()
	}

	pvcName, err := RenderPVCName(preFix, config.Name, config.Namespace)
	if err != nil {
		logger.Error(err, "Unable to calculate hash")
		return nil, fmt.Errorf("unable to calculate hash: %w", err)
	}
	logger = logger.WithValues("pvc_name", pvcName)

	driver := drivers.GetDriver(provisioner)
	if driver == nil {
		logger.Info("Driver not found")
		return nil, errors.New("driver not found: " + provisioner)
	}

	pvc, err := driver.GetPVCStub(pvcName, config.Namespace, config.Spec.StorageClassName)
	if err != nil {
		logger.Error(err, "Unable to init a PVC", "provisioner", provisioner)
		return nil, fmt.Errorf("unable to init a PVC: %w", err)
	}

	pvc.Finalizers = []string{RenderFinalizer(config.Name)}

	pvc.Labels = map[string]string{
		"discoblocks": config.Name,
	}

	capacity, err := resource.ParseQuantity(config.Spec.Capacity)
	if err != nil {
		logger.Error(err, "Capacity is invalid")
		return nil, fmt.Errorf("capacity is invalid [%s]: %w", config.Spec.Capacity, err)
	}

	pvc.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceStorage: capacity,
		},
	}

	pvc.Spec.AccessModes = config.Spec.AccessModes
	if len(pvc.Spec.AccessModes) == 0 {
		pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}

	return pvc, nil
}
