package mcode

import (
	"context"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// TODO(viewloader): replace this file with the shared view loader.

// nodeLoader is how the host loads a dynamic node: the ELF interpreter the
// binary names, the host file presenting it, and the resolved directories its
// libraries load from. A static node has none.
type nodeLoader struct {
	interp, source string
	libraries      []string
}

func findLoader(ctx context.Context, node string) (nodeLoader, error) {
	var loader nodeLoader
	file, err := elf.Open(node)
	if err != nil {
		return loader, err
	}
	defer file.Close()
	for _, prog := range file.Progs {
		if prog.Type == elf.PT_INTERP {
			raw, err := io.ReadAll(io.LimitReader(prog.Open(), 4096))
			if err != nil {
				return loader, err
			}
			loader.interp = strings.TrimRight(string(raw), "\x00")
		}
	}
	if loader.interp == "" {
		return loader, nil
	}
	if !filepath.IsAbs(loader.interp) || filepath.Clean(loader.interp) != loader.interp {
		return loader, fmt.Errorf("node's interpreter %q is not a clean absolute path", loader.interp)
	}
	if loader.source, err = filepath.EvalSymlinks(loader.interp); err != nil {
		return loader, err
	}
	// The view resolves the same libraries through LD_LIBRARY_PATH alone.
	command := exec.CommandContext(ctx, loader.source, "--list", node)
	command.Env = []string{}
	out, err := command.Output()
	if err != nil {
		return loader, fmt.Errorf("node's libraries: %w", err)
	}
	for line := range strings.Lines(string(out)) {
		_, resolved, ok := strings.Cut(strings.TrimSpace(line), " => ")
		if !ok {
			continue
		}
		library, _, _ := strings.Cut(resolved, " (")
		if !filepath.IsAbs(library) {
			return loader, fmt.Errorf("node's library %s", strings.TrimSpace(line))
		}
		dir, err := libraryDir(library)
		if err != nil {
			return loader, err
		}
		if !slices.Contains(loader.libraries, dir) {
			loader.libraries = append(loader.libraries, dir)
		}
	}
	return loader, nil
}

// libraryDir returns the resolved directory holding library. The library's
// name may link only to other names in that directory, so presenting the
// directory presents the library.
func libraryDir(library string) (string, error) {
	dir, err := filepath.EvalSymlinks(filepath.Dir(library))
	if err != nil {
		return "", err
	}
	name := filepath.Base(library)
	for range 8 {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		if info.Mode().IsRegular() {
			return dir, nil
		}
		if info.Mode()&os.ModeSymlink == 0 {
			break
		}
		if name, err = os.Readlink(filepath.Join(dir, name)); err != nil || strings.ContainsRune(name, '/') {
			break
		}
	}
	return "", fmt.Errorf("node's library %s does not resolve within its directory", library)
}
