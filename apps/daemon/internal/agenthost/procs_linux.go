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

	"golang.org/x/sys/unix"
)

// task is one running task, a thread of a process, as /proc lists it.
type task struct {
	tgid, tid int
	uids      [4]uint32 // real, effective, saved and file-system
}

func (r UIDRange) has(id uint32) bool { return id >= r.First && id-r.First < r.Count }

// processTable lists the host's tasks. Tests replace it.
type processTable interface {
	// tasks returns every task that runs, each thread of each process; a
	// zombie runs nothing and is left out.
	tasks() ([]task, error)
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

// procfs is the host's /proc.
type procfs struct{}

// errGone is a process or task that has ended.
var errGone = errors.New("ended")

func (procfs) tasks() ([]task, error) {
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

func procPath(pid int, name ...string) string {
	return "/proc/" + strconv.Itoa(pid) + "/" + strings.Join(name, "/")
}

// status is what a status file in /proc reports.
type status struct {
	state string
	uids  [4]uint32
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
		case "Uid":
			uids = strings.Fields(value)
		}
	}
	if len(uids) != 4 {
		return s, fmt.Errorf("%s has no uids", path)
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
