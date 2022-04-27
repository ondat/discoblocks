package utils

import (
	"errors"
	"regexp"
	"strconv"
)

var (
	capacityPattern    = regexp.MustCompile(`^(\d+)(m|Mi|g|Gi|t|Ti|p|Pi)$`)
	errInvalidCapacity = errors.New("invalid capacity")
)

func ParseCapacity(capacity string) (uint16, string, error) {
	parts := capacityPattern.FindAllSubmatch([]byte(capacity), -1)
	if parts == nil {
		return 0, "", errInvalidCapacity
	}
	sizeInt, err := strconv.Atoi(string(parts[0][1]))
	if err != nil {
		return 0, "", errors.New("invalid size")
	}

	return uint16(sizeInt), string(parts[0][2]), nil
}
