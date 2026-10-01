//go:build linux

package claudesdk

import (
	"debug/elf"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// closureLoader finds the ELF interpreter that the dynamic binaries share and
// checks that the interpreter's directory holds every library they need, so
// the view loads nothing from the sandbox's files.
func closureLoader(binaries ...string) (viewLoader, error) {
	var loader viewLoader
	var pending []string
	for _, binary := range binaries {
		interp, needed, err := elfDependencies(binary)
		if err != nil {
			return viewLoader{}, err
		}
		if interp == "" {
			if len(needed) != 0 {
				return viewLoader{}, fmt.Errorf("%s needs libraries but no interpreter", binary)
			}
			continue
		}
		if loader.Interp != "" && loader.Interp != interp {
			return viewLoader{}, fmt.Errorf("the closure binaries need different ELF interpreters")
		}
		loader.Interp = interp
		pending = append(pending, needed...)
	}
	if loader.Interp == "" {
		return viewLoader{}, nil
	}
	source, err := filepath.EvalSymlinks(loader.Interp)
	if err != nil {
		return viewLoader{}, err
	}
	loader.Source, loader.LibDir = source, filepath.Dir(source)
	seen := map[string]bool{}
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[name] {
			continue
		}
		seen[name] = true
		path, err := libraryFile(loader.LibDir, name)
		if err != nil {
			return viewLoader{}, fmt.Errorf("library %s is not in %s", name, loader.LibDir)
		}
		_, needed, err := elfDependencies(path)
		if err != nil {
			return viewLoader{}, err
		}
		pending = append(pending, needed...)
	}
	return loader, nil
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
