//go:build !linux

package processbroker

import "errors"

// ErrUnsupported is returned on platforms without the process broker.
var ErrUnsupported = errors.New("processbroker: unsupported on this platform")

// Broker is unavailable on this platform.
type Broker struct{}

// Start returns ErrUnsupported.
func Start(Config) (*Broker, error) { return nil, ErrUnsupported }

// Close does nothing.
func (*Broker) Close() error { return nil }
