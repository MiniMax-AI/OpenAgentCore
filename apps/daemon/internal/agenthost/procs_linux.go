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

// task is one running task, a thread of a process, as /proc lists it.
type task struct {
	tgid, tid int
	uids      [4]uint32 // real, effective, saved and file-system
}

// in reports whether t holds a uid in r.
func (t task) in(r UIDRange) bool { return holds(t.uids, r) }

func holds(uids [4]uint32, r UIDRange) bool {
	return r.has(uids[0]) || r.has(uids[1]) || r.has(uids[2]) || r.has(uids[3])
}

func (r UIDRange) has(id uint32) bool { return id >= r.First && id-r.First < r.Count }

// processTable lists the host's tasks and ends them. Tests replace it.
type processTable interface {
	// tasks returns every task that runs, each thread of each process; a
	// zombie runs nothing and is left out.
	tasks() ([]task, error)
	// end kills, while t still holds a uid in r, the init of t's PID
	// namespace when that is a view's, which ends every process in it, and
	// t's process when t is in the agent host's own namespace. It never
	// signals a process that reused a pid.
	end(t task, r UIDRange) error
}

// heldUIDs returns the uids in r that a running task holds.
func heldUIDs(procs processTable, r UIDRange) (map[uint32]bool, error) {
	tasks, err := procs.tasks()
	if err != nil {
		return nil, err
	}
	held := map[uint32]bool{}
	for _, t := range tasks {
		for _, id := range t.uids {
			if r.has(id) {
				held[id] = true
			}
		}
	}
	return held, nil
}

// endProcesses ends every task that holds a uid in r and scans again until
// none runs or bound passes.
func endProcesses(procs processTable, r UIDRange, bound time.Duration) error {
	deadline := time.Now().Add(bound)
	for {
		tasks, err := procs.tasks()
		if err != nil {
			return err
		}
		var held []task
		for _, t := range tasks {
			if t.in(r) {
				held = append(held, t)
			}
		}
		if len(held) == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("%d tasks still hold Session uids after %s", len(held), bound)
		}
		ended := map[int]bool{}
		for _, t := range held {
			if ended[t.tgid] {
				continue
			}
			ended[t.tgid] = true
			if err := procs.end(t, r); err != nil {
				return err
			}
		}
		time.Sleep(sweepPoll)
	}
}

// procfs is the host's /proc, which must be the agent host's own PID
// namespace's.
type procfs struct{}

// errGone is a process or task that has ended.
var errGone = errors.New("ended")

func (procfs) tasks() ([]task, error) {
	self, err := readStatus("/proc/self/status")
	if err != nil {
		return nil, err
	}
	if len(self.nspid) != 1 {
		return nil, errors.New("/proc is not the agent host's PID namespace's")
	}
	pids, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var list []task
	for _, p := range pids {
		pid, err := strconv.Atoi(p.Name())
		if err != nil || pid <= 0 {
			continue
		}
		tids, err := os.ReadDir(procPath(pid, "task"))
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range tids {
			tid, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			s, err := readStatus(procPath(pid, "task", e.Name(), "status"))
			if errors.Is(err, errGone) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if s.running() {
				list = append(list, task{tgid: pid, tid: tid, uids: s.uids})
			}
		}
	}
	return list, nil
}

func (procfs) end(t task, r UIDRange) error {
	fd, err := unix.PidfdOpen(t.tgid, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("pidfd of %d: %w", t.tgid, err)
	}
	// The pid may name another process since the scan. What /proc shows under
	// it belongs to the process fd pins while that process exists, which each
	// read confirms afterwards; once it has ended, the signal reaches nothing.
	s, err := readStatus(procPath(t.tgid, "task", strconv.Itoa(t.tid), "status"))
	if err == nil {
		err = exists(fd)
	}
	if err == nil && (!s.running() || !holds(s.uids, r)) {
		err = errGone
	}
	if err == nil && len(s.nspid) > 1 {
		fd, err = namespaceInit(fd, t.tgid, len(s.nspid))
	}
	if errors.Is(err, errGone) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("kill: %w", err)
	}
	return nil
}

// namespaceInit takes fd, the pidfd of process pid, and returns a pidfd of the
// init of its PID namespace, depth namespaces below /proc's. On an error it
// closes every pidfd. The init is the process's nearest ancestor whose pid in
// its own namespace is 1: no process in a view can enter another namespace,
// so every ancestor up to the init shares the namespace. Each step pins the
// parent and then confirms that the child still exists and still has that
// parent, so the walk never follows a reused pid.
func namespaceInit(fd, pid, depth int) (int, error) {
	for {
		s, err := readStatus(procPath(pid, "status"))
		if err == nil {
			err = exists(fd)
		}
		if err == nil && (len(s.nspid) != depth || s.ppid <= 0) {
			err = fmt.Errorf("process %d is outside its view's PID namespace", pid)
		}
		if err != nil {
			unix.Close(fd)
			return -1, err
		}
		if s.nspid[depth-1] == 1 {
			return fd, nil
		}
		parent, err := unix.PidfdOpen(s.ppid, 0)
		if errors.Is(err, unix.ESRCH) {
			continue // the parent has ended and the process has a new one
		}
		if err != nil {
			unix.Close(fd)
			return -1, fmt.Errorf("pidfd of %d: %w", s.ppid, err)
		}
		again, err := readStatus(procPath(pid, "status"))
		if err == nil {
			err = exists(fd)
		}
		if err != nil {
			unix.Close(parent)
			unix.Close(fd)
			return -1, err
		}
		if again.ppid != s.ppid {
			unix.Close(parent)
			continue
		}
		unix.Close(fd)
		fd, pid = parent, s.ppid
	}
}

// exists returns nil while the process fd pins exists, as a zombie too, and
// errGone once it has been reaped.
func exists(fd int) error {
	err := unix.PidfdSendSignal(fd, 0, nil, 0)
	if errors.Is(err, unix.ESRCH) {
		return errGone
	}
	return err
}

func procPath(pid int, name ...string) string {
	return "/proc/" + strconv.Itoa(pid) + "/" + strings.Join(name, "/")
}

// status is what a status file in /proc reports.
type status struct {
	state string
	ppid  int
	uids  [4]uint32
	nspid []int // the pid in each PID namespace, from /proc's to the task's own
}

func (s status) running() bool {
	return !strings.HasPrefix(s.state, "Z") && !strings.HasPrefix(s.state, "X")
}

// readStatus reads a status file in /proc. It returns errGone when the
// process or task has ended.
func readStatus(path string) (status, error) {
	var s status
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH) {
		return s, errGone
	}
	if err != nil {
		return s, err
	}
	var uids []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, value, _ := strings.Cut(sc.Text(), ":")
		switch key {
		case "State":
			s.state = strings.TrimSpace(value)
		case "PPid":
			if s.ppid, err = strconv.Atoi(strings.TrimSpace(value)); err != nil {
				return s, fmt.Errorf("%s: PPid %q", path, value)
			}
		case "Uid":
			uids = strings.Fields(value)
		case "NSpid":
			for _, f := range strings.Fields(value) {
				n, err := strconv.Atoi(f)
				if err != nil {
					return s, fmt.Errorf("%s: NSpid %q", path, value)
				}
				s.nspid = append(s.nspid, n)
			}
		}
	}
	if len(uids) != 4 || len(s.nspid) == 0 {
		return s, fmt.Errorf("%s has no uids or NSpid", path)
	}
	for i, f := range uids {
		id, err := strconv.ParseUint(f, 10, 32)
		if err != nil {
			return s, fmt.Errorf("%s: uid %q", path, f)
		}
		s.uids[i] = uint32(id)
	}
	return s, nil
}
