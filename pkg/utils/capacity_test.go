package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseCapacity(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		capacity      string
		expectedSize  uint16
		expectedUnit  string
		expectedError error
	}{
		"empty": {
			expectedError: errInvalidCapacity,
		},
		"missing size": {
			capacity:      "Gi",
			expectedError: errInvalidCapacity,
		},
		"missing unit": {
			capacity:      "500",
			expectedError: errInvalidCapacity,
		},
		"invalid unit": {
			capacity:      "500G",
			expectedError: errInvalidCapacity,
		},
		"ok": {
			capacity:     "500Gi",
			expectedSize: 500,
			expectedUnit: "Gi",
		},
	}

	for n, c := range cases {
		c := c
		t.Run(n, func(t *testing.T) {
			t.Parallel()

			size, unit, err := ParseCapacity(c.capacity)

			assert.Equal(t, c.expectedSize, size, "invalid size")
			assert.Equal(t, c.expectedUnit, unit, "invalid unit")
			assert.Equal(t, c.expectedError, err, "invalid size")
		})
	}
}
