package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetSidecarStub(t *testing.T) {
	_, err := RenderSidecar()

	assert.Nil(t, err, "invalid sidecar template")
}
