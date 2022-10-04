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

func TestGetMountPointIndex(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		pattern       string
		mountPoint    string
		expectedIndex int
	}{
		"without order": {
			pattern:       "/media/discoblocks/foo",
			mountPoint:    "/media/discoblocks/foo",
			expectedIndex: 0,
		},
		"without high order": {
			pattern:       "/media/discoblocks/foo",
			mountPoint:    "/media/discoblocks/foo-99",
			expectedIndex: 99,
		},
		"with order": {
			pattern:       "/media/discoblocks/foo-%d",
			mountPoint:    "/media/discoblocks/foo-0",
			expectedIndex: 0,
		},
		"with higher order": {
			pattern:       "/media/discoblocks/foo-%d",
			mountPoint:    "/media/discoblocks/foo-99",
			expectedIndex: 99,
		},
		"not found": {
			pattern:       "/media/discoblocks/foo",
			mountPoint:    "/media/discoblocks/bar",
			expectedIndex: -1,
		},
		"too many": {
			pattern:       "/media/discoblocks/foo",
			mountPoint:    "/media/discoblocks/foo-1000",
			expectedIndex: -1,
		},
	}

	for n, c := range cases {
		c := c
		t.Run(n, func(t *testing.T) {
			t.Parallel()

			index := GetMountPointIndex(c.pattern, "", c.mountPoint)

			assert.Equal(t, c.expectedIndex, index, "invalid index")
		})
	}
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
