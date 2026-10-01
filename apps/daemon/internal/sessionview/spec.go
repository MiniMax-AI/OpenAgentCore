package sessionview

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

// Spec declares one view and the process it runs.
type Spec struct {
	World    World
	Private  []PrivateDir
	Overlays []Overlay
	Shim     Shim
	Process  Process
	Network  Network
	// StagingParent is an existing absolute host directory, such as the Session directory, in which the view creates its staging directory and removes it at teardown.
	StagingParent string
}

// World starts serving the view's root file system on dev, a /dev/fuse connection that the launcher has already mounted as mount describes. The server runs in the daemon, outside the view, and never mounts or unmounts anything. sessionview closes dev after Stop returns.
type World func(dev *os.File, mount WorldMount) (WorldServer, error)

// WorldServer is a running world.
type WorldServer interface {
	// Stop returns once serving has ended. sessionview calls it after the view has exited, when the kernel has aborted the connection.
	Stop() error
}

// WorldMount describes how the launcher mounted the world.
type WorldMount struct {
	// Options are the FUSE mount options, apart from the launcher's fd number.
	Options []string
	// Flags are the mount flags.
	Flags []string
	// Mountpoints are the view paths the launcher mounts over. The world presents each one, and each ancestor as a directory, with no symlinks and a stable identity for the view's lifetime, whether or not the sandbox has the path. A mountpoint that disappears or changes identity detaches what is mounted on it.
	Mountpoints []Mountpoint
}

// Mountpoint is a view path the world presents as a directory or a regular file.
type Mountpoint struct {
	Path string
	Dir  bool
}

// PrivateDir binds a host directory at agent.ViewPrivateRoot/<Name>. A writable directory is never executable.
type PrivateDir struct {
	Name     string
	HostDir  string
	Writable bool
	Exec     bool
}

// Overlay presents a trusted, immutable host file or directory at an absolute view path.
type Overlay struct {
	Path   string
	Source string
	Exec   bool
}

// Shim presents a static binary at agent.ViewPrivateRoot/agent.ViewShimName/<name> for each name and binds it over each absolute view path.
type Shim struct {
	Binary string
	Names  []string
	Paths  []string
}

// Process is the one process the view runs.
type Process struct {
	Path   string
	Args   []string
	Env    []string
	Dir    string
	UID    uint32
	GID    uint32
	Groups []uint32
	// A nil Stdin, Stdout or Stderr is a pipe whose other end the View exposes.
	Stdin, Stdout, Stderr *os.File
	// Grace is how long processes left in the view when the process exits have to exit, counted from the first TERM the view sent them, before the view ends. Zero ends the view at once.
	Grace time.Duration
}

// Network configures the view's network namespace, which has only loopback up.
type Network struct {
	// Setup, when set, runs with the view's network namespace before the process starts. sessionview closes netns after Setup returns; what Setup opens in the namespace belongs to the caller.
	Setup func(netns *os.File) error
}

// Exit is how the process ended.
type Exit struct {
	Code       int
	Signal     syscall.Signal
	CoreDumped bool
}

func (s *Spec) validate() error {
	if s.World == nil {
		return invalid("world is required")
	}
	if info, err := hostSource(s.StagingParent); err != nil {
		return err
	} else if !info.IsDir() {
		return invalid("staging parent %s is not a directory", s.StagingParent)
	}
	names := map[string]bool{}
	for _, d := range s.Private {
		if !isComponent(d.Name) || d.Name == agent.ViewShimName || names[d.Name] {
			return invalid("private directory name %q", d.Name)
		}
		names[d.Name] = true
		if d.Writable && d.Exec {
			return invalid("private directory %q is both writable and executable", d.Name)
		}
		if info, err := hostSource(d.HostDir); err != nil {
			return err
		} else if !info.IsDir() {
			return invalid("private directory %s is not a directory", d.HostDir)
		}
	}
	var claimed []string
	for _, o := range s.Overlays {
		if err := viewPath(o.Path); err != nil {
			return err
		}
		if _, err := hostSource(o.Source); err != nil {
			return err
		}
		claimed = append(claimed, o.Path)
	}
	if len(s.Shim.Names) > 0 || len(s.Shim.Paths) > 0 {
		if info, err := hostSource(s.Shim.Binary); err != nil {
			return err
		} else if info.IsDir() {
			return invalid("shim %s is a directory", s.Shim.Binary)
		}
	}
	names = map[string]bool{}
	for _, n := range s.Shim.Names {
		if !isComponent(n) || names[n] {
			return invalid("shim name %q", n)
		}
		names[n] = true
	}
	for _, p := range s.Shim.Paths {
		if err := viewPath(p); err != nil {
			return err
		}
		claimed = append(claimed, p)
	}
	for i, a := range claimed {
		for _, b := range claimed[i+1:] {
			if a == b || within(a, b) || within(b, a) {
				return invalid("view paths %s and %s overlap", a, b)
			}
		}
	}
	return s.Process.validate()
}

func (p *Process) validate() error {
	if !isViewAbs(p.Path) || !isViewAbs(p.Dir) {
		return invalid("process path %q and directory %q must be absolute and clean", p.Path, p.Dir)
	}
	if len(p.Args) == 0 {
		return invalid("process argv is empty")
	}
	if p.Grace < 0 {
		return invalid("process grace %v is negative", p.Grace)
	}
	if p.UID == 0 || p.GID == 0 {
		return invalid("process must not run as uid or gid 0")
	}
	for _, a := range p.Args {
		if strings.IndexByte(a, 0) >= 0 {
			return invalid("process argument contains NUL")
		}
	}
	for _, e := range p.Env {
		if strings.IndexByte(e, 0) >= 0 || strings.IndexByte(e, '=') <= 0 {
			return invalid("process environment entry %q", e)
		}
	}
	return nil
}

func viewPath(p string) error {
	if !isViewAbs(p) || p == "/" {
		return invalid("view path %q must be absolute, clean and not /", p)
	}
	if agent.ViewReserved(p) {
		return invalid("view path %s is inside a tree the launcher builds", p)
	}
	return nil
}

// hostSource checks that a trusted host path is a directory or a regular file.
func hostSource(p string) (os.FileInfo, error) {
	if !filepath.IsAbs(p) {
		return nil, invalid("host path %q must be absolute", p)
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, &Error{Kind: ErrInvalidSpec, Op: "stat", Path: p, Err: err}
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, invalid("host path %s is neither a directory nor a regular file", p)
	}
	return info, nil
}

func isViewAbs(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && strings.IndexByte(p, 0) < 0
}

func isComponent(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}

// within reports whether p is strictly beneath dir.
func within(p, dir string) bool {
	return strings.HasPrefix(p, dir+"/")
}
