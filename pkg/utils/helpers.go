package utils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
)

const maxName = 253

const defaultMountPattern = "/media/discoblocks/%s-%d"

// RenderMountPoint calculates mount point
func RenderMountPoint(pattern, name string, index int) string {
	if pattern == "" {
		return fmt.Sprintf(defaultMountPattern, name, index)
	}

	if index != 0 && !strings.Contains(pattern, "%d") {
		pattern += "-%d"
	}

	if !strings.Contains(pattern, "%d") {
		return pattern
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

// RenderResourceName calculates resource name
func RenderResourceName(prefix bool, elems ...string) (string, error) {
	builder := strings.Builder{}

	if len(elems) == 0 {
		return "", errors.New("missing name elements")
	}

	if prefix {
		builder.WriteString("discoblocks")
	} else {
		builder.WriteString(elems[0])
	}

	for _, e := range elems {
		hash, err := Hash(e)
		if err != nil {
			return "", fmt.Errorf("unable to calculate hash of %s: %w", e, err)
		}

		builder.WriteString(fmt.Sprintf("-%d", hash))
	}

	l := builder.Len()
	if l > maxName {
		l = maxName
	}

	return builder.String()[:l], nil
}

// RenderUniqueLabel renders DiskConfig label
func RenderUniqueLabel(id string) string {
	hash, err := Hash(id)
	if err != nil {
		panic("Unable to calculate hash, better to say good bye!")
	}

	return fmt.Sprintf("discoblocks/%d", hash)
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

// GetNamePrefix returns the prefix by availability type
func GetNamePrefix(am discoblocksondatiov1.AvailabilityMode, configUID, nodeName string) string {
	switch am {
	case discoblocksondatiov1.ReadWriteOnce:
		return time.Now().String()
	case discoblocksondatiov1.ReadWriteSame:
		return configUID
	case discoblocksondatiov1.ReadWriteDaemon:
		return nodeName
	default:
		panic("Missing availability mode implementation: " + string(am))
	}
}

// ReadFileOrDie reads the file or die
func ReadFileOrDie(path string) []byte {
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		panic(err)
	}

	return content
}
