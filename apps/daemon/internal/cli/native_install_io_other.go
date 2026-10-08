//go:build !linux

package cli

import "os"

func nativeAvailableSpace(string) (uint64, error) { return 0, requireNativePlatform() }
func nativeDiskFull(error) bool                   { return false }
func nativeTerminal(*os.File) bool                { return false }
