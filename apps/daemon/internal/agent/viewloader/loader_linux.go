//go:build linux

package viewloader

import (
	"debug/elf"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

// For returns the fragment for the host binaries. The dynamic ones must share
// one ELF interpreter, and the interpreter's directory must hold every library
// they need. Any other layout is unsupported.
func For(binaries ...string) (Fragment, error) {
	interp := ""
	var pending []string
	for _, binary := range binaries {
		next, needed, err := elfDependencies(binary)
		if err != nil {
			return Fragment{}, err
		}
		if next == "" {
			if len(needed) != 0 {
				return Fragment{}, unsupported("%s needs libraries but no interpreter", binary)
			}
			continue
		}
		if interp != "" && interp != next {
			return Fragment{}, unsupported("the binaries need different ELF interpreters")
		}
		interp = next
		pending = append(pending, needed...)
	}
	if interp == "" {
		return Fragment{}, nil
	}
	source, err := filepath.EvalSymlinks(interp)
	if err != nil {
		return Fragment{}, err
	}
	libDir := filepath.Dir(source)
	seen := map[string]bool{}
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[name] {
			continue
		}
		seen[name] = true
		path, err := libraryFile(libDir, name)
		if err != nil {
			return Fragment{}, unsupported("library %s is not in %s: %v", name, libDir, err)
		}
		_, needed, err := elfDependencies(path)
		if err != nil {
			return Fragment{}, err
		}
		pending = append(pending, needed...)
	}
	return fragment(interp, source, libDir), nil
}

func elfDependencies(path string) (string, []string, error) {
	file, err := elf.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	interp := ""
	for _, prog := range file.Progs {
		if prog.Type == elf.PT_INTERP {
			raw, err := io.ReadAll(prog.Open())
			if err != nil {
				return "", nil, err
			}
			interp = strings.TrimRight(string(raw), "\x00")
		}
	}
	needed, err := file.ImportedLibraries()
	return interp, needed, err
}

// libraryFile resolves name in dir through links to other names in dir only,
// because the view presents dir at another path.
func libraryFile(dir, name string) (string, error) {
	for range 8 {
		if !filepath.IsLocal(name) || filepath.Base(name) != name {
			return "", fmt.Errorf("library link %q leaves its directory", name)
		}
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			return path, nil
		}
		if name, err = os.Readlink(path); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("library %s has too many links", name)
}

func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", agent.ErrUnsupportedOperation, fmt.Sprintf(format, args...))
}
