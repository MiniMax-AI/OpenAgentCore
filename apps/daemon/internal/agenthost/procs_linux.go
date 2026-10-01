//go:build linux

package agenthost

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// sweepBound bounds how long Sweep waits for the processes it killed.
	sweepBound = 10 * time.Second
	// sweepPoll is the pause between Sweep's scans.
	sweepPoll = 50 * time.Millisecond
)

// hostProcess is one process /proc lists, with its real, effective, saved and
// file-system uids.
type hostProcess struct {
	pid  int
	uids [4]uint32
}

// in reports whether p holds a uid in r.
func (p hostProcess) in(r UIDRange) bool {
	for _, id := range p.uids {
		if id >= r.First && id-r.First < r.Count {
			return true
		}
	}
	return false
}

// processTable lists and kills the host's processes. Tests replace it.
type processTable interface {
	// list returns every process that still runs; a zombie runs nothing and
	// is left out.
	list() ([]hostProcess, error)
	// kill sends SIGKILL to the process p names when it still holds a uid in
	// r, and never to a process that reused p's pid.
	kill(p hostProcess, r UIDRange) error
}

// heldUIDs returns the uids in r that a running process holds.
func heldUIDs(procs processTable, r UIDRange) (map[uint32]bool, error) {
	list, err := procs.list()
	if err != nil {
		return nil, err
	}
	held := map[uint32]bool{}
	for _, p := range list {
		for _, id := range p.uids {
			if id >= r.First && id-r.First < r.Count {
				held[id] = true
			}
		}
	}
	return held, nil
}

// endProcesses kills every process that holds a uid in r, scanning again
// until none remains or bound passes. A process in r can only fork processes
// of its own uid, so each scan finds what the previous round's processes
// started.
func endProcesses(procs processTable, r UIDRange, bound time.Duration) error {
	deadline := time.Now().Add(bound)
	for {
		list, err := procs.list()
		if err != nil {
			return err
		}
		var held []hostProcess
		for _, p := range list {
			if p.in(r) {
				held = append(held, p)
			}
		}
		if len(held) == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%d processes still hold Session uids after %s", len(held), bound)
		}
		for _, p := range held {
			if err := procs.kill(p, r); err != nil {
				return err
			}
		}
		time.Sleep(sweepPoll)
	}
}

// procfs is the host's /proc.
type procfs struct{}

func (procfs) list() ([]hostProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var list []hostProcess
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		p, running, err := readProcess(pid)
		if err != nil {
			return nil, err
		}
		if running {
			list = append(list, p)
		}
	}
	return list, nil
}

func (procfs) kill(p hostProcess, r UIDRange) error {
	fd, err := unix.PidfdOpen(p.pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("pidfd of %d: %w", p.pid, err)
	}
	defer unix.Close(fd)
	// The pid may name another process since the scan. The uids read after
	// the pidfd opened are its process's while it runs; once it has ended,
	// the signal reaches nothing.
	q, running, err := readProcess(p.pid)
	if err != nil || !running || !q.in(r) {
		return err
	}
	if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("kill %d: %w", p.pid, err)
	}
	return nil
}

// readProcess reads pid's uids from /proc/<pid>/status. running is false when
// the process has ended or is a zombie.
func readProcess(pid int) (p hostProcess, running bool, err error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	p.pid = pid
	var state string
	var uids []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, value, _ := strings.Cut(sc.Text(), ":")
		switch key {
		case "State":
			state = strings.TrimSpace(value)
		case "Uid":
			uids = strings.Fields(value)
		}
	}
	if len(uids) != 4 {
		return p, false, fmt.Errorf("/proc/%d/status has no uids", pid)
	}
	for i, s := range uids {
		id, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return p, false, fmt.Errorf("/proc/%d/status uid %q", pid, s)
		}
		p.uids[i] = uint32(id)
	}
	return p, !strings.HasPrefix(state, "Z") && !strings.HasPrefix(state, "X"), nil
}
