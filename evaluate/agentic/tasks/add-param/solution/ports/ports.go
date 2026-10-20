package ports

import (
	"fmt"
	"strconv"
)

// Parse converts s to a TCP port number no greater than max.
func Parse(s string, max int) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	if n < 1 || n > max {
		return 0, fmt.Errorf("port %d out of range", n)
	}
	return n, nil
}
