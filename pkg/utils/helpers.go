package utils

import (
	"fmt"
	"math/big"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

const defaultMountPattern = "/media/discoblocks/%s-%d"

// TODO on this way on case of multiple discoblocks on a pod,
// all service would capture all disks leads to redundant data
const metricsServiceTemplate = `kind: Service
apiVersion: v1
metadata:
  name: %s
  namespace: %s
  annotations:
    prometheus.io/path: "/metrics"
    prometheus.io/scrape: "true"
    prometheus.io/port:   "9100"
spec:
  ports:
  - name: node-exporter
    protocol: TCP
    port: 9100
    targetPort: 9100`

// TODO limit filesystem reports to discoblocks (ignored-mount-points)
const metricsTeamplate = `name: discoblocks-metrics
image: bitnami/node-exporter:1.3.1
ports:
- containerPort: 9100
  protocol: TCP
command:
- /opt/bitnami/node-exporter/bin/node_exporter
- --collector.disable-defaults
- --collector.filesystem`

// TODO maybe a config map for templates makes sense
const sidecarTeamplate = `name: discoblocks-manager
image: alpine:3.15.4
command:
- sleep
- infinity
volumeMounts:
- name: dev
  mountPath: /host/dev
securityContext:
  allowPrivilegeEscalation: true
  privileged: true`

// RenderMountPoint calculates mount point
func RenderMountPoint(pattern, name string, index int) string {
	if pattern == "" {
		return fmt.Sprintf(defaultMountPattern, name, 0)
	}

	return fmt.Sprintf(pattern, index)
}

// RenderFinalizer calculates finalizer name
func RenderFinalizer(name string, extras ...string) string {
	finalizer := fmt.Sprintf("discoblocks.io/%s", name)

	for _, e := range extras {
		finalizer = finalizer + "-" + e
	}

	return finalizer
}

// RenderPVCName calculates PVC name
func RenderPVCName(elems ...string) (string, error) {
	builder := strings.Builder{}
	builder.WriteString("discoblocks")

	for _, e := range elems {
		hash, err := Hash(e)
		if err != nil {
			return "", fmt.Errorf("unable to calculate hash of %s: %w", e, err)
		}

		builder.WriteString(fmt.Sprintf("-%d", hash))
	}

	return builder.String(), nil
}

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

// RenderManagerSidecar returns the manager sidecar
func RenderManagerSidecar() (*corev1.Container, error) {
	sidecar := corev1.Container{}
	if err := yaml.Unmarshal([]byte(sidecarTeamplate), &sidecar); err != nil {
		return nil, err
	}

	return &sidecar, nil
}

// IsContainsAll finds for a contains all b
func IsContainsAll(a, b map[string]string) bool {
	match := 0
	for key, value := range b {
		if a[key] == value {
			match++
		}
	}

	return match == len(b)
}

// ParsePrometheusMetric parses Prometheus metrisc details
func ParsePrometheusMetric(metric string) (map[string]*dto.MetricFamily, error) {
	var parser expfmt.TextParser

	okErr := expfmt.ParseError{Line: 1, Msg: "unexpected end of input stream"}

	mf, err := parser.TextToMetricFamilies(strings.NewReader(metric))
	if err == okErr && mf != nil {
		err = nil
	}

	return mf, err
}

// ParsePrometheusMetricValue parses Prometheus metrisc value
func ParsePrometheusMetricValue(metric string) (float64, error) {
	parts := strings.Split(metric, " ")

	const floatBase = 10

	flt, _, err := big.ParseFloat(parts[len(parts)-1], floatBase, 0, big.ToNearestEven)
	f, _ := flt.Float64()

	return f, err
}
