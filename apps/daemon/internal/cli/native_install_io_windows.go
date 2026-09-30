package cli

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func nativeAvailableSpace(directory string) (uint64, error) {
	name, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		return 0, err
	}
	var free, total, available uint64
	err = windows.GetDiskFreeSpaceEx(name, &available, &total, &free)
	return available, err
}
func nativeDiskFull(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL) || errors.Is(err, windows.ERROR_DISK_QUOTA_EXCEEDED)
}
func nativeTerminal(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}
