package utils

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	discoblocksondatiov1 "github.com/ondat/discoblocks/api/v1"
	"github.com/reiver/go-telnet"
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

// FetchDiskInfo calls 'df' on the remote address
func FetchDiskInfo(addr string) (map[string]float64, error) {
	content, err := Telnet(addr)
	if err != nil {
		return nil, fmt.Errorf("unable to call endpoint %s: %w", addr, err)
	}

	if len(content) <= 1 {
		return nil, nil
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

// Telnet calls endpoint and reads response
func Telnet(addr string) (lines []string, err error) {
	lines = []string{}

	var conn *telnet.Conn
	conn, err = telnet.DialTo(addr)
	if err != nil {
		return
	}
	defer conn.Close()

	const five = 5
	timeout, cancel := context.WithTimeout(context.Background(), time.Second*five)
	defer cancel()

	reader := bufio.NewReader(conn)
	for {
		select {
		case <-timeout.Done():
			err = timeout.Err()
			return
		default:
			var line string
			line, err = reader.ReadString('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
					return
				}

				return
			}

			if line != "" {
				lines = append(lines, line[:len(line)-1])
			}
		}
	}
}
