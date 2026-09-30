//go:build linux

package node

import (
	"math"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
)

type healthCgroup struct {
	dir, mount string
	v2         bool
}

const healthMaxCgroupDepth = 128

// Resolve the process membership through mountinfo instead of assuming a fixed
// cgroup mount layout. A subtree-only mount hides ancestors, so it is unknown.
func findHealthCgroup(groups, mounts, controller string) (healthCgroup, bool) {
	var group, unified string
	found := false
	for _, line := range strings.Split(strings.TrimSpace(groups), "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) != 3 || !cleanHealthPath(fields[2]) {
			return healthCgroup{}, false
		}
		id, err := strconv.ParseUint(fields[0], 10, 32)
		if err != nil || (id == 0) != (fields[1] == "") {
			return healthCgroup{}, false
		}
		if fields[0] == "0" && fields[1] == "" {
			if unified != "" {
				return healthCgroup{}, false
			}
			unified = fields[2]
		}
		if slices.Contains(strings.Split(fields[1], ","), controller) {
			if found {
				return healthCgroup{}, false
			}
			group, found = fields[2], true
		}
	}
	v2 := !found && unified != ""
	if v2 {
		group = unified
	} else if !found {
		// A valid membership list without this controller has no such limit.
		return healthCgroup{}, true
	}
	for _, line := range strings.Split(mounts, "\n") {
		before, after, ok := strings.Cut(line, " - ")
		left, right := strings.Fields(before), strings.Fields(after)
		if !ok || len(left) < 6 || len(right) < 3 {
			continue
		}
		if v2 && right[0] != "cgroup2" || !v2 && (right[0] != "cgroup" || !slices.Contains(strings.Split(right[2], ","), controller)) {
			continue
		}
		root, mount := unescapeMountPath(left[3]), unescapeMountPath(left[4])
		if root != "/" || !cleanHealthPath(mount) {
			continue
		}
		return healthCgroup{dir: path.Join(mount, group), mount: mount, v2: v2}, true
	}
	return healthCgroup{}, false
}

func cleanHealthPath(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.HasSuffix(p, " (deleted)")
}

func unescapeMountPath(p string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(p)
}

func (g healthCgroup) visibleRoot(read func(string) ([]byte, error)) bool {
	if g.v2 {
		// cgroup.type exists only below the real root, including a namespace root.
		_, err := read(path.Join(g.mount, "cgroup.type"))
		return os.IsNotExist(err)
	}
	_, err := read(path.Join(g.mount, "release_agent"))
	return err == nil
}

func (g healthCgroup) cpuLimit(read func(string) ([]byte, error)) (float64, bool) {
	limit := math.Inf(1)
	if g.dir == "" {
		return limit, true
	}
	if !g.visibleRoot(read) {
		return 0, false
	}
	for dir, depth := g.dir, 0; depth < healthMaxCgroupDepth; dir, depth = path.Dir(dir), depth+1 {
		var quota, period string
		if g.v2 {
			data, ok := g.v2Limit(read, dir, "cpu", "cpu.max")
			if !ok {
				return 0, false
			}
			fields := strings.Fields(data)
			if len(fields) != 2 {
				return 0, false
			}
			quota, period = fields[0], fields[1]
		} else {
			q, qe := read(path.Join(dir, "cpu.cfs_quota_us"))
			p, pe := read(path.Join(dir, "cpu.cfs_period_us"))
			if qe != nil || pe != nil {
				return 0, false
			}
			quota, period = strings.TrimSpace(string(q)), strings.TrimSpace(string(p))
		}
		p, err := strconv.ParseInt(period, 10, 64)
		if err != nil || p <= 0 {
			return 0, false
		}
		if !(g.v2 && quota == "max" || !g.v2 && quota == "-1") {
			q, err := strconv.ParseInt(quota, 10, 64)
			if err != nil || q <= 0 {
				return 0, false
			}
			limit = min(limit, float64(q)/float64(p))
		}
		if dir == g.mount {
			return limit, true
		}
	}
	return 0, false
}

func (g healthCgroup) v2Limit(read func(string) ([]byte, error), dir, controller, file string) (string, bool) {
	data, err := read(path.Join(dir, file))
	if err == nil {
		// The real v2 root has no resource limit file; a namespace root can hide ancestors.
		return strings.TrimSpace(string(data)), dir != g.mount
	}
	if !os.IsNotExist(err) {
		return "", false
	}
	controllers, err := read(path.Join(dir, "cgroup.controllers"))
	if err != nil {
		return "", false
	}
	// Root has no quota/max file. At other levels absence is legitimate only
	// when the controller is not enabled there. Still inspect every ancestor.
	unlimited := "max"
	if controller == "cpu" {
		unlimited = "max 1"
	}
	return unlimited, dir == g.mount || !slices.Contains(strings.Fields(string(controllers)), controller)
}
