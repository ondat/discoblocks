package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderMetricsService(t *testing.T) {
	_, err := RenderMetricsService("name", "namespace")

	assert.Nil(t, err, "invalid metrics service template")
}

func TestRenderMetricsSidecar(t *testing.T) {
	_, err := RenderMetricsSidecar()

	assert.Nil(t, err, "invalid sidecar template")
}

func TestRenderMountJob(t *testing.T) {
	_, err := RenderHostJob("name", "namespace", "nodeName", "dev", "fs", "mountPoint", []string{"c1", "c2"}, func() (string, error) { return "command", nil })

	assert.Nil(t, err, "invalid mount job template")
}
