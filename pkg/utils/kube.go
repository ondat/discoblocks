package utils

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/ondat/discoblocks/pkg/drivers"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"
)

// Used for Yaml indentation
const hostCommandPrefix = "\n          "

var hostCommandReplacePattern = regexp.MustCompile(`\n`)

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

// TODO replace nixery image and make sockets configurable
// TODO make socket mounts depends on target system
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
        image: nixery.dev/shell/gawk/gnugrep/gnused/coreutils-full/cri-tools/docker-client
        env:
        - name: MOUNT_POINT
          value: "%s"
        - name: CONTAINER_IDS
          value: "%s"
        - name: PVC_NAME
          value: "%s"
        - name: DEV
          value: "%s"
        - name: FS
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
  backoffLimit: 10
`

// RenderMetricsService returns the metrics service
func RenderMetricsService(name, namespace string) (*corev1.Service, error) {
	service := corev1.Service{}
	if err := yaml.Unmarshal([]byte(fmt.Sprintf(metricsServiceTemplate, name, namespace)), &service); err != nil {
		return nil, fmt.Errorf("unable to unmarshal service: %w", err)
	}

	return &service, nil
}

// RenderMetricsSidecar returns the metrics sidecar
func RenderMetricsSidecar() (*corev1.Container, error) {
	sidecar := corev1.Container{}
	if err := yaml.Unmarshal([]byte(metricsTeamplate), &sidecar); err != nil {
		return nil, fmt.Errorf("unable to unmarshal container: %w", err)
	}

	return &sidecar, nil
}

// RenderHostJob returns the job executed on host
func RenderHostJob(pvcName, namespace, nodeName, dev, fs, mountPoint string, containerIDs []string, getCommand func() (string, error)) (*batchv1.Job, error) {
	hostCommand, err := getCommand()
	if err != nil {
		return nil, fmt.Errorf("unable to get command: %w", err)
	}

	hostCommand = string(hostCommandReplacePattern.ReplaceAll([]byte(hostCommand), []byte(hostCommandPrefix)))

	jobName, err := RenderResourceName(fmt.Sprintf("%d", time.Now().UnixNano()), pvcName, namespace)
	if err != nil {
		return nil, fmt.Errorf("unable to render resource name: %w", err)
	}

	template := fmt.Sprintf(hostJobTemplate, jobName, namespace, nodeName, mountPoint, strings.Join(containerIDs, " "), pvcName, dev, fs, hostCommand)

	job := batchv1.Job{}
	if err := yaml.Unmarshal([]byte(template), &job); err != nil {
		println(template)
		return nil, fmt.Errorf("unable to unmarshal job: %w", err)
	}

	return &job, nil
}

// NewPVC constructs a new PVC instance
func NewPVC(config *discoblocksondatiov1.DiskConfig, prefix string, driver *drivers.Driver) (*corev1.PersistentVolumeClaim, error) {
	pvcName, err := RenderResourceName(prefix, config.Name, config.Namespace)
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

	capacity, err := resource.ParseQuantity(config.Spec.Capacity)
	if err != nil {
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
