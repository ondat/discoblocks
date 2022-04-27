package utils

import (
	"fmt"

	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
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
func RenderFinalizer(id string) string {
	return fmt.Sprintf("discoblocks.io/%s", id)
}

// RenderPVCName calculates PVC name
func RenderPVCName(pod string, config *discoblocksondatiov1.DiskConfig) string {
	return fmt.Sprintf("discoblocks-%d-%d-%d", Hash(pod), Hash(config.CreationTimestamp.String()), Hash(config.Namespace+config.Name))
}

// RenderSidecar returns the sidecar
func RenderSidecar() (*corev1.Container, error) {
	sidecar := corev1.Container{}
	if err := yaml.Unmarshal([]byte(sidecarTeamplate), &sidecar); err != nil {
		return nil, err
	}

	return &sidecar, nil
}

// IsContainsString finds string in array
func IsContainsString(s string, arr []string) bool {
	for _, e := range arr {
		if e == s {
			return true
		}
	}

	return false
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
