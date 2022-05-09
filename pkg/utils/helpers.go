package utils

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

const defaultMountPattern = "/media/discoblocks/%s-%d"

// TODO use pause container instead
const sidecarTeamplate = `name: discoblocks-sidecar
image: alpine:3.15.4
command:
- sleep
- infinity
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

// RenderSidecar returns the sidecar
func RenderSidecar() (*corev1.Container, error) {
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
