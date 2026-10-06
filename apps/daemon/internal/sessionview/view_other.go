//go:build !linux

package sessionview

import (
	"context"
	"os"
	"syscall"
)

// Init does nothing outside Linux.
func Init() {}

// Probe reports that views need Linux.
func Probe() error { return ErrUnsupported }

// Recover reports that view cgroups need Linux.
func Recover(string) error { return ErrUnsupported }

// Start reports that views need Linux.
func Start(context.Context, Spec) (*View, error) { return nil, ErrUnsupported }

// View is a running view. Outside Linux none exists.
type View struct{}

func (*View) Presentation() Presentation  { return Presentation{} }
func (*View) Wait() (Exit, error)         { return Exit{}, ErrUnsupported }
func (*View) Signal(syscall.Signal) error { return ErrUnsupported }
func (*View) Close() error                { return nil }
func (*View) Stdin() *os.File             { return nil }
func (*View) Stdout() *os.File            { return nil }
func (*View) Stderr() *os.File            { return nil }
func (*View) Relay() *os.File             { return nil }
