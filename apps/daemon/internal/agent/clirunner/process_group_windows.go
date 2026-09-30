//go:build windows

package clirunner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The child cannot execute user code until it belongs to the non-inheritable Job.
// Closing the daemon's last Job handle also kills children after a daemon crash.
func startProcessGroup(opts StartOptions) (*Process, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	ctx, cancel := context.WithCancel(opts.Parent)
	cmd := exec.CommandContext(ctx, opts.Binary, opts.Args...)
	cmd.Dir = opts.Dir
	if len(opts.Env) > 0 {
		cmd.Env = append([]string{}, opts.Env...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP}
	group := &ownedJob{handle: job}
	cmd.Cancel = group.cancel
	p := &Process{Cmd: cmd, ctx: ctx, cancel: cancel, done: make(chan struct{}), cancelProcess: group.cancel}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		cancel()
		windows.CloseHandle(job)
		return nil, err
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		cancel()
		windows.CloseHandle(job)
		stdout.Close()
		stdoutWriter.Close()
		return nil, err
	}
	p.Stdout, p.Stderr = stdout, stderr
	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	if opts.NeedStdin {
		p.Stdin, err = cmd.StdinPipe()
	}
	if err == nil {
		err = cmd.Start()
	}
	stdoutWriter.Close()
	stderrWriter.Close()
	if err == nil {
		group.mu.Lock()
		if group.cancelled {
			err = context.Canceled
		}
		process, openErr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
		if openErr != nil {
			err = openErr
		}
		if err == nil {
			err = windows.AssignProcessToJobObject(job, process)
		}
		if openErr == nil {
			windows.CloseHandle(process)
		}
		if err == nil {
			err = resumeInitialThread(uint32(cmd.Process.Pid))
		}
		group.mu.Unlock()
	}
	if err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		cancel()
		_ = group.cancel()
		group.finish()
		closePipe(p.Stdin)
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("clirunner: owned Windows process: %w", err)
	}
	var waitErr error
	p.waitProcess = func() error { <-p.done; stdout.Close(); stderr.Close(); return waitErr }
	go func() {
		waitErr = cmd.Wait()
		// A successful leader exit cannot leave detached descendants behind.
		_ = group.cancel()
		if group.finish() {
			close(p.done)
		}
	}()
	return p, nil
}

func resumeInitialThread(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return err
	}
	return fmt.Errorf("clirunner: suspended process thread unavailable: %w", err)
}

type ownedJob struct {
	mu             sync.Mutex
	handle         windows.Handle
	finished       bool
	cancelled      bool
	processes      []windows.Handle
	observationErr error
}

func (g *ownedJob) cancel() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finished {
		return os.ErrProcessDone
	}
	if !g.cancelled {
		// Hold process handles before termination removes them from the Job list.
		g.processes, g.observationErr = jobProcessHandles(g.handle)
	}
	g.cancelled = true
	return windows.TerminateJobObject(g.handle, 1)
}

// Job accounting can reach zero before the process objects become signaled.
// Keep ownership until both observations confirm cleanup; never wait with mu held.
func (g *ownedJob) finish() bool {
	g.mu.Lock()
	processes, observationErr := g.processes, g.observationErr
	g.mu.Unlock()
	if observationErr != nil {
		return false
	}
	for _, process := range processes {
		status, err := windows.WaitForSingleObject(process, windows.INFINITE)
		if err != nil || status != windows.WAIT_OBJECT_0 {
			return false
		}
	}
	type accounting struct {
		UserTime, KernelTime, PeriodUserTime, PeriodKernelTime           int64
		PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
	}
	for {
		var info accounting
		err := windows.QueryInformationJobObject(g.handle, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
		if err == nil && info.ActiveProcesses == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.finished = true
	for _, process := range processes {
		windows.CloseHandle(process)
	}
	windows.CloseHandle(g.handle)
	return true
}
