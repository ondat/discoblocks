package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderMountPoint(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		pattern            string
		name               string
		index              int
		expectedMountPoint string
	}{
		"default": {
			pattern:            "",
			name:               "foo",
			index:              1,
			expectedMountPoint: "/media/discoblocks/foo-1",
		},
		"given-with-order": {
			pattern:            "/bar-%d",
			name:               "foo",
			index:              1,
			expectedMountPoint: "/bar-1",
		},
		"given-without-order-first": {
			pattern:            "/bar",
			name:               "foo",
			index:              0,
			expectedMountPoint: "/bar",
		},
		"given-without-order-second": {
			pattern:            "/bar",
			name:               "foo",
			index:              1,
			expectedMountPoint: "/bar-1",
		},
	}

	for n, c := range cases {
		c := c
		t.Run(n, func(t *testing.T) {
			t.Parallel()

			mountPoint := RenderMountPoint(c.pattern, c.name, c.index)

			assert.Equal(t, c.expectedMountPoint, mountPoint, "invalid mount point")
		})
	}
}

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

func TestIsGreater(t *testing.T) {
	assert.True(t, IsGreater("foo-1", ""))
	assert.True(t, IsGreater("foo-10", "foo-2"))
}
