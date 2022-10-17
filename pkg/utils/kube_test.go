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
	_, err := RenderMetricsSidecar(true)

	assert.Nil(t, err, "invalid sidecar template")
}
