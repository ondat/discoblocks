package diskinfo

import (
	"bufio"
	"context"
	"errors"
	"io"
	"time"

	"github.com/reiver/go-telnet"
)

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
