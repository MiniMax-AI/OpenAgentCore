//go:build linux

package worldfs_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/worldfs"
	"github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/fileservicetest"
	"golang.org/x/sys/unix"
)

// sandboxTree is a sandbox whose /bin is a symlink to usr/bin, as on merged-/usr distributions.
func sandboxTree(t *testing.T) string {
	t.Helper()
	backing := t.TempDir()
	for _, d := range []string{"usr/bin", "etc", "data"} {
		if err := os.MkdirAll(filepath.Join(backing, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(backing, "usr/bin/existing"), "sandbox file")
	writeFile(t, filepath.Join(backing, "usr/bin/env"), "sandbox env")
	if err := os.Symlink("usr/bin", filepath.Join(backing, "bin")); err != nil {
		t.Fatal(err)
	}
	return backing
}

func TestPresentation(t *testing.T) {
	requireFUSE(t)
	backing := sandboxTree(t)
	before := snapshot(t, backing)
	m, err := serve(t, backing, 0,
		sessionview.Mountpoint{Path: "/.oac/harness", Dir: true},
		sessionview.Mountpoint{Path: "/etc/oac-overlay", Dir: true},
		sessionview.Mountpoint{Path: "/bin/sh"},
		sessionview.Mountpoint{Path: "/usr/bin/env"},
	)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if want := []string{"/.oac/harness", "/etc/oac-overlay", "/usr/bin/sh", "/usr/bin/env"}; !slices.Equal(m.present.Targets, want) {
		t.Errorf("Targets = %q, want %q", m.present.Targets, want)
	}
	if want := []sessionview.PresentedLink{{Path: "/bin", Target: "usr/bin"}}; !slices.Equal(m.present.Links, want) {
		t.Errorf("Links = %+v, want %+v", m.present.Links, want)
	}
	if want := []string{"/.oac"}; !slices.Equal(m.present.Synthesized, want) {
		t.Errorf("Synthesized = %q, want %q", m.present.Synthesized, want)
	}

	at := func(p string) string { return filepath.Join(m.dir, p) }
	wantNode(t, at(".oac"), unix.S_IFDIR|0o555)
	wantNode(t, at(".oac/harness"), unix.S_IFDIR|0o555)
	wantNode(t, at("etc/oac-overlay"), unix.S_IFDIR|0o555)
	wantNode(t, at("usr/bin/sh"), unix.S_IFREG|0o444)
	wantNode(t, at("usr/bin/env"), unix.S_IFREG|0o444)
	if l, err := os.Readlink(at("bin")); err != nil || l != "usr/bin" {
		t.Errorf("readlink bin = %q, %v", l, err)
	}
	if b, err := os.ReadFile(at("bin/existing")); err != nil || string(b) != "sandbox file" {
		t.Errorf("bin/existing = %q, %v", b, err)
	}
	if b, err := os.ReadFile(at("bin/env")); err != nil || len(b) != 0 {
		t.Errorf("presented bin/env = %q, %v; want the empty mountpoint", b, err)
	}
	if names := list(t, at("usr/bin")); !slices.Equal(names, []string{"env", "existing", "sh"}) {
		t.Errorf("usr/bin lists %q", names)
	}
	if names := list(t, m.dir); !slices.Equal(names, []string{".oac", "bin", "data", "etc", "usr"}) {
		t.Errorf("root lists %q", names)
	}

	for _, op := range []struct {
		name string
		err  error
	}{
		{"remove a mountpoint", os.Remove(at("usr/bin/env"))},
		{"rename a pinned symlink", os.Rename(at("bin"), at("bin2"))},
		{"replace a pinned directory", unix.Rename(at("data"), at("usr"))},
		{"create in a synthetic directory", os.Mkdir(at(".oac/x"), 0o755)},
		{"write a mountpoint", os.WriteFile(at("usr/bin/sh"), nil, 0o644)},
	} {
		if !errors.Is(op.err, syscall.EPERM) {
			t.Errorf("%s: %v, want EPERM", op.name, op.err)
		}
	}
	writeFile(t, at("bin/new"), "through the link")
	if err := os.Remove(filepath.Join(backing, "usr/bin/new")); err != nil {
		t.Errorf("file written through the presented link: %v", err)
	}
	if after := snapshot(t, backing); !maps.Equal(before, after) {
		t.Errorf("sandbox changed:\nbefore %v\nafter  %v", before, after)
	}
}

// A directory with presented children lists every name once, sends at most one ReadDir per READDIR, and keeps its offsets across a rewind. In hidden every sandbox entry is a mountpoint; paged has one mountpoint, which the sandbox lacks.
func TestPresentedPaging(t *testing.T) {
	requireFUSE(t)
	backing := t.TempDir()
	dirs := []string{"hidden", "paged"}
	var mps []sessionview.Mountpoint
	want := map[string][]string{}
	for _, dir := range dirs {
		if err := os.Mkdir(filepath.Join(backing, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range 120 {
			name := fmt.Sprintf("%s-%03d-%s", dir, i, strings.Repeat("x", 60))
			writeFile(t, filepath.Join(backing, dir, name), "")
			want[dir] = append(want[dir], name)
			if dir == "hidden" {
				mps = append(mps, sessionview.Mountpoint{Path: "/hidden/" + name})
			}
		}
		mps = append(mps, sessionview.Mountpoint{Path: "/" + dir + "/absent"})
		want[dir] = append(want[dir], "absent")
		slices.Sort(want[dir])
	}
	m, err := serve(t, backing, 0, mps...)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	for _, dir := range dirs {
		fd, err := unix.Open(filepath.Join(m.dir, dir), unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
		var pages [][]string
		var offs []int64
		for {
			// Each getdents is one READDIR.
			before := m.readDirs.Load()
			names := getdents(t, fd)
			if n := m.readDirs.Load() - before; n > 1 {
				t.Errorf("%s: a READDIR sent %d ReadDir requests", dir, n)
			}
			if len(names) == 0 {
				break
			}
			pages = append(pages, names)
			off, err := unix.Seek(fd, 0, io.SeekCurrent)
			if err != nil {
				t.Fatal(err)
			}
			offs = append(offs, off)
		}
		if got := slices.Sorted(slices.Values(slices.Concat(pages...))); !slices.Equal(got, want[dir]) {
			t.Errorf("%s lists %d names, want each of %d once", dir, len(got), len(want[dir]))
		}
		if len(pages) < 3 {
			t.Fatalf("%s lists in %d pages, want at least 3", dir, len(pages))
		}
		if _, err := unix.Seek(fd, 0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		getdents(t, fd)
		if _, err := unix.Seek(fd, offs[1], io.SeekStart); err != nil {
			t.Fatal(err)
		}
		if got := getdents(t, fd); !slices.Equal(got, pages[2]) {
			t.Errorf("%s: after a rewind, the offset of the second page lists %d names, want the %d of the third page", dir, len(got), len(pages[2]))
		}
	}
}

func getdents(t *testing.T, fd int) []string {
	t.Helper()
	buf := make([]byte, 64<<10)
	n, err := unix.Getdents(fd, buf)
	if err != nil {
		t.Fatalf("getdents: %v", err)
	}
	_, _, names := unix.ParseDirent(buf[:n], -1, nil)
	return names
}

func TestPresentationLoop(t *testing.T) {
	requireFUSE(t)
	backing := t.TempDir()
	for _, l := range [][2]string{{"b", "a"}, {"a", "b"}} {
		if err := os.Symlink(l[0], filepath.Join(backing, l[1])); err != nil {
			t.Fatal(err)
		}
	}
	_, err := serve(t, backing, 0, sessionview.Mountpoint{Path: "/a/x"})
	if !errors.Is(err, worldfs.ErrMountpoint) || !errors.Is(err, syscall.ELOOP) {
		t.Fatalf("Serve = %v, want ErrMountpoint and ELOOP", err)
	}
}

func TestTopologyChanged(t *testing.T) {
	requireFUSE(t)
	backing := sandboxTree(t)
	m, err := serve(t, backing, 0, sessionview.Mountpoint{Path: "/bin/sh"})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if err := os.Remove(filepath.Join(backing, "bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("usr/local/bin", filepath.Join(backing, "bin")); err != nil {
		t.Fatal(err)
	}
	if l, err := os.Readlink(filepath.Join(m.dir, "bin")); err != nil || l != "usr/bin" {
		t.Errorf("readlink bin = %q, %v; want the pinned target", l, err)
	}
	waitLost(t, m.world, worldfs.ErrTopologyChanged)
	wantNode(t, filepath.Join(m.dir, "usr/bin/sh"), unix.S_IFREG|0o444)
}

func TestViewEditsWorkspace(t *testing.T) {
	requireFUSE(t)
	if err := sessionview.Probe(); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	backing := sandboxTree(t)
	srv, err := fileservicetest.New(backing)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	harness := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyFile(t, self, filepath.Join(harness, "harness"))

	w := worldfs.New(fileservicetest.Export, srv.Dial)
	v, err := sessionview.Start(context.Background(), sessionview.Spec{
		World:   w.Serve,
		Private: []sessionview.PrivateDir{{Name: "harness", HostDir: harness, Exec: true}},
		Shim:    sessionview.Shim{Binary: filepath.Join(harness, "harness"), Names: []string{"sh"}, Paths: []string{"/bin/sh"}},
		Process: sessionview.Process{
			Path:   "/.oac/harness/harness",
			Args:   []string{"harness"},
			Env:    []string{helperEnv + "=1"},
			Dir:    "/data",
			UID:    1000,
			GID:    1000,
			Stderr: os.Stderr,
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer v.Close()
	out, err := io.ReadAll(v.Stdout())
	if err != nil {
		t.Fatal(err)
	}
	if exit, err := v.Wait(); err != nil || exit != (sessionview.Exit{}) {
		t.Fatalf("Wait = %+v, %v; output %s", exit, err, out)
	}
	if strings.TrimSpace(string(out)) != shimMarker {
		t.Errorf("/bin/sh in the view printed %q, want the shim", out)
	}
	if !slices.Contains(v.Presentation().Targets, "/usr/bin/sh") {
		t.Errorf("Targets = %q, want the shim at /usr/bin/sh", v.Presentation().Targets)
	}
	if b, err := os.ReadFile(filepath.Join(backing, "data/f.txt")); err != nil || string(b) != "edited in the view" {
		t.Errorf("data/f.txt = %q, %v", b, err)
	}
	if _, err := os.Lstat(filepath.Join(backing, "data/f.tmp")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("data/f.tmp left behind: %v", err)
	}
	for _, p := range []string{".oac", "proc", "dev", "usr/bin/sh"} {
		if _, err := os.Lstat(filepath.Join(backing, p)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the view created %s in the sandbox: %v", p, err)
		}
	}
}

func wantNode(t *testing.T, p string, mode uint32) {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Lstat(p, &st); err != nil {
		t.Errorf("lstat %s: %v", p, err)
		return
	}
	if st.Mode != mode || st.Uid != 0 || st.Gid != 0 {
		t.Errorf("%s: mode %o owner %d:%d, want %o 0:0", p, st.Mode, st.Uid, st.Gid, mode)
	}
}

func list(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

// snapshot describes every entry under dir by type, permissions and content.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		desc := info.Mode().String()
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			desc += " -> " + l
		case info.Mode().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			desc += " " + string(b)
		}
		m[strings.TrimPrefix(p, dir)] = desc
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o755); err != nil {
		t.Fatal(err)
	}
}
