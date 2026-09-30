//go:build windows

package clirunner

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var isProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

// A PID list alone cannot confirm exit: retain handles until termination signals
// each process. Check membership after opening to avoid a recycled foreign PID.
func jobProcessHandles(job windows.Handle) ([]windows.Handle, error) {
	pids, err := jobProcessIDs(job)
	if err != nil {
		return nil, err
	}
	var handles []windows.Handle
	for _, pid := range pids {
		process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			continue
		}
		if err != nil {
			return handles, err
		}
		var belongs int32
		ok, _, queryErr := isProcessInJob.Call(uintptr(process), uintptr(job), uintptr(unsafe.Pointer(&belongs)))
		if ok == 0 {
			windows.CloseHandle(process)
			return handles, queryErr
		}
		if belongs == 0 {
			windows.CloseHandle(process)
			continue
		}
		handles = append(handles, process)
	}
	return handles, nil
}

func jobProcessIDs(job windows.Handle) ([]uintptr, error) {
	type processList struct {
		Assigned, Count uint32
		First           uintptr
	}
	for size := 64; ; size *= 2 {
		// uintptr storage keeps the variable-length Windows structure aligned.
		storage := make([]uintptr, size+2)
		list := (*processList)(unsafe.Pointer(&storage[0]))
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(list)), uint32(len(storage)*int(unsafe.Sizeof(uintptr(0)))), nil)
		if errors.Is(err, windows.ERROR_MORE_DATA) || (err == nil && list.Count < list.Assigned) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if list.Count > uint32(size) {
			return nil, fmt.Errorf("clirunner: invalid Job process count")
		}
		return append([]uintptr(nil), unsafe.Slice(&list.First, int(list.Count))...), nil
	}
}
