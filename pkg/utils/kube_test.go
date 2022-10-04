package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderMetricsSidecar(t *testing.T) {
	_, err := RenderMetricsSidecar()

	assert.Nil(t, err, "invalid sidecar template")
}
