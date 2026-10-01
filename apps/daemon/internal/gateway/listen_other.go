//go:build !linux

package gateway

import (
	"net"
	"os"
)

// listen needs Linux network namespaces.
func listen(*os.File, []int) ([]net.Listener, error) { return nil, ErrUnsupported }
