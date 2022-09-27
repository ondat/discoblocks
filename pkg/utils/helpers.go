package utils

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
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

// GetMountPointIndex calculates index by mount point
func GetMountPointIndex(pattern, name, mountPoint string) int {
	if mountPoint == RenderMountPoint(pattern, name, 0) {
		return 0
	}

	const maxDisks = 500

	for i := 1; i < maxDisks; i++ {
		if mountPoint == RenderMountPoint(pattern, name, i) {
			return i
		}
	}

	return -1
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
func RenderUniqueLabel(name string) string {
	hash, err := Hash(name)
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

// ParsePrometheusMetric parses Prometheus metrisc details
func ParsePrometheusMetric(metric string) (map[string]*dto.MetricFamily, error) {
	var parser expfmt.TextParser

	okErr := expfmt.ParseError{Line: 1, Msg: "unexpected end of input stream"}

	mf, err := parser.TextToMetricFamilies(strings.NewReader(metric))
	if err == okErr && mf != nil {
		err = nil
	}

	return mf, err
}

// ParsePrometheusMetricValue parses Prometheus metrisc value
func ParsePrometheusMetricValue(metric string) (float64, error) {
	parts := strings.Split(metric, " ")

	const floatBase = 10

	flt, _, err := big.ParseFloat(parts[len(parts)-1], floatBase, 0, big.ToNearestEven)
	f, _ := flt.Float64()

	return f, err
}

// CompareStringNaturalOrder compares string in natural order
func CompareStringNaturalOrder(a, b string) bool {
	numberRegex := regexp.MustCompile(`\d+`)

	convert := func(i string) string {
		numbers := map[string]bool{}
		for _, n := range numberRegex.FindAll([]byte(i), -1) {
			numbers[string(n)] = true
		}

		for n := range numbers {
			i = strings.ReplaceAll(i, n, fmt.Sprintf("%09s", n))
		}

		return i
	}

	return convert(a) < convert(b)
}
