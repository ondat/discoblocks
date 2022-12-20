package diskinfo

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Fetch calls 'df' on the remote address across a tunnel
func Fetch(name, namespace string) (map[string]float64, error) {
	addr, err := getProxy(name, namespace)
	if err != nil {
		return nil, fmt.Errorf("unable to find proxy: %w", err)
	}

	content, err := Telnet(addr)
	if err != nil {
		return nil, fmt.Errorf("unable to call endpoint %s: %w", addr, err)
	}

	if len(content) <= 1 {
		return nil, errors.New("empty content")
	}

	content = content[1:]

	diskInfo := map[string]float64{}
	for _, line := range content {
		parts := strings.Fields(line)

		const six = 6
		if len(parts) != six {
			return nil, fmt.Errorf("unable to find valid disk info: %s", line)
		}

		const tt = 32
		used, err := strconv.ParseFloat(parts[4][:len(parts[4])-1], tt)
		if err != nil {
			return nil, fmt.Errorf("unable to parse float by %s: %w", parts[4][:len(parts[4])-1], err)
		}

		diskInfo[parts[5]] = used
	}

	return diskInfo, nil
}
