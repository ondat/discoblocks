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
