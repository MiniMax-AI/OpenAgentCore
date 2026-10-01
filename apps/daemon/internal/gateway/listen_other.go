//go:build !linux

package gateway

import (
	"net"
	"os"
)

// listen needs Linux network namespaces.
func listen(*os.File, []int) ([]*net.TCPListener, error) { return nil, ErrUnsupported }
