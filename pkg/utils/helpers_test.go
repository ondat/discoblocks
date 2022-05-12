package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetSidecarStub(t *testing.T) {
	_, err := RenderMetricsSidecar()

	assert.Nil(t, err, "invalid sidecar template")
}

func TestParsePrometheusMetric(t *testing.T) {
	mf, err := ParsePrometheusMetric(`node_filesystem_free_bytes{device="/dev/nvme1n1",fstype="ext4",mountpoint="/media/discoblocks/sample-0"} 1.020678144e+09`)

	assert.Nil(t, err, "invalid metric")

	mountpoint := ""
	for _, m := range mf["node_filesystem_free_bytes"].Metric {
		for _, l := range m.Label {
			if *l.Name == "mountpoint" {
				mountpoint = *l.Value
			}
		}
	}
	assert.Equal(t, "/media/discoblocks/sample-0", mountpoint)
}

func TestParsePrometheusMetricValue(t *testing.T) {
	value, err := ParsePrometheusMetricValue(`node_filesystem_free_bytes{device="/dev/nvme1n1",fstype="ext4",mountpoint="/media/discoblocks/sample-0"} 1.020678144e+09`)

	assert.Nil(t, err, "invalid metric")
	assert.Equal(t, float64(1020678144), value)
}
