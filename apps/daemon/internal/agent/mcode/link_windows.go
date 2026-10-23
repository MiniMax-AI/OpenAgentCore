//go:build windows

package mcode

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// linkDirectory makes link resolve to the existing directory target.
//
// It creates a directory junction. A directory symbolic link needs Developer
// Mode or elevation on Windows; a junction needs neither, and os.Readlink
// returns its target, so the native Skill root is verified as on Unix.
func linkDirectory(target, link string) error {
	if !filepath.IsAbs(target) {
		return fmt.Errorf("mcode: junction target must be absolute")
	}
	if err := os.Mkdir(link, 0700); err != nil {
		return err
	}
	if err := setJunction(link, target); err != nil {
		_ = os.Remove(link)
		return err
	}
	return nil
}

// setJunction writes a mount point reparse point to the empty directory link.
func setJunction(link, target string) error {
	path, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	data := junctionReparseData(target)
	var returned uint32
	return windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &data[0], uint32(len(data)), nil, 0, &returned, nil)
}

// junctionReparseData encodes REPARSE_DATA_BUFFER for IO_REPARSE_TAG_MOUNT_POINT:
// the NT substitute name \??\<target> and the print name <target>, each
// followed by a UTF-16 NUL that the lengths exclude.
func junctionReparseData(target string) []byte {
	substitute := utf16.Encode([]rune(`\??\` + target))
	printName := utf16.Encode([]rune(target))
	names := make([]uint16, 0, len(substitute)+len(printName)+2)
	names = append(append(append(append(names, substitute...), 0), printName...), 0)
	data := make([]byte, 16+2*len(names))
	binary.LittleEndian.PutUint32(data[0:], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(data[4:], uint16(8+2*len(names)))
	binary.LittleEndian.PutUint16(data[8:], 0)
	binary.LittleEndian.PutUint16(data[10:], uint16(2*len(substitute)))
	binary.LittleEndian.PutUint16(data[12:], uint16(2*len(substitute)+2))
	binary.LittleEndian.PutUint16(data[14:], uint16(2*len(printName)))
	for i, unit := range names {
		binary.LittleEndian.PutUint16(data[16+2*i:], unit)
	}
	return data
}
