package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
)

// This is release content, never an input file for installation options.
type nativeBundle struct {
	Schema        int                        `json:"schema"`
	DaemonVersion string                     `json:"daemon_version"`
	OS            string                     `json:"os"`
	Arch          string                     `json:"arch"`
	Components    map[string]nativeComponent `json:"components"`
}
type nativeComponent struct {
	Version string                `json:"version"`
	Files   map[string]nativeFile `json:"files"`
}
type nativeFile struct {
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable"`
}

var nativePins = func() map[string]string {
	out := map[string]string{"node": "22.22.0"}
	for name, spec := range nativeHarnesses {
		out[name] = spec.Version
	}
	return out
}()

func readNativeJSON(file string, value any) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 16<<20+1))
	if err != nil || len(raw) > 16<<20 {
		return errors.New("installation metadata unavailable")
	}
	return decodeEnvironmentJSON(raw, value)
}

func readNativeBundle(directory string, selected []string) (nativeBundle, error) {
	var b nativeBundle
	if readNativeJSON(filepath.Join(directory, "bundle.json"), &b) != nil {
		return b, errors.New("install: use the native installer distribution containing bundle.json")
	}
	if b.Schema != 1 || b.DaemonVersion != Version || b.OS != runtime.GOOS || b.Arch != runtime.GOARCH {
		return b, errors.New("install: distribution version or platform mismatch; use a matching current release")
	}
	for _, name := range append([]string{"node"}, selected...) {
		c, ok := b.Components[name]
		if !ok || c.Version != nativePins[name] || len(c.Files) == 0 {
			return b, fmt.Errorf("install: pinned %s component missing from this distribution", name)
		}
		for file, info := range c.Files {
			if !validBundlePath(file) || len(info.SHA256) != 64 {
				return b, errors.New("install: invalid component file manifest")
			}
			if _, err := hex.DecodeString(info.SHA256); err != nil {
				return b, errors.New("install: invalid component digest")
			}
		}
	}
	return b, nil
}

func validBundlePath(name string) bool {
	return name != "." && path.Clean(name) == name && !strings.ContainsAny(name, "\\\x00\r\n:") && filepath.IsLocal(filepath.FromSlash(name)) && name != ".oac-install.json"
}

func nativeComponentRoot(root, name string) string { return filepath.Join(root, "components", name) }

func componentReceipt(root string) (nativeComponent, error) {
	var c nativeComponent
	err := readNativeJSON(filepath.Join(root, ".oac-install.json"), &c)
	return c, err
}

var errNativeComponentMismatch = errors.New("installed files do not match the verified release")

func checkComponentFiles(ctx context.Context, directory string, c nativeComponent) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for name, expected := range c.Files {
		if !validBundlePath(name) {
			return errors.New("invalid installed path")
		}
		f, err := root.Open(filepath.FromSlash(name))
		if err != nil {
			return err
		}
		info, statErr := f.Stat()
		if statErr != nil {
			f.Close()
			return statErr
		}
		if !info.Mode().IsRegular() {
			f.Close()
			return errNativeComponentMismatch
		}
		h := sha256.New()
		_, copyErr := nativeCopy(ctx, h, f)
		f.Close()
		if copyErr != nil {
			return copyErr
		}
		if hex.EncodeToString(h.Sum(nil)) != expected.SHA256 || (runtime.GOOS != "windows" && expected.Executable && info.Mode().Perm()&0100 == 0) {
			return errNativeComponentMismatch
		}
	}
	return nil
}

// A complete component is published once. An interruption cannot turn a partial
// copy into an installation; a subsequent run can reuse an already-published one.
func installNativeComponent(ctx context.Context, source, root, name string, expected nativeComponent) error {
	dest := nativeComponentRoot(root, name)
	if _, err := os.Lstat(dest); err == nil {
		got, e := componentReceipt(dest)
		if e != nil || !reflect.DeepEqual(got, expected) {
			return fmt.Errorf("install: existing %s is incompatible; preserve it and use a separate installation directory", name)
		}
		if err := checkComponentFiles(ctx, dest, got); err != nil {
			if errors.Is(err, errNativeComponentMismatch) || errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("install: existing %s is incomplete or modified; reinstall separately", name)
			}
			return fmt.Errorf("install: cannot verify existing %s: %w", name, err)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".install-"+name+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	src, err := os.OpenRoot(filepath.Join(source, "components", name))
	if err != nil {
		return err
	}
	defer src.Close()
	var required uint64
	for name := range expected.Files {
		if !validBundlePath(name) {
			return errors.New("install: invalid component path")
		}
		info, err := src.Stat(filepath.FromSlash(name))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || uint64(info.Size()) > ^uint64(0)-required {
			return errors.New("install: invalid component size")
		}
		required += uint64(info.Size())
	}
	if err = requireNativeSpace(parent, required); err != nil {
		return err
	}
	for name, expectedFile := range expected.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validBundlePath(name) {
			return errors.New("install: invalid component path")
		}
		in, err := src.Open(filepath.FromSlash(name))
		if err != nil {
			return errors.New("install: component file unavailable")
		}
		info, err := in.Stat()
		if err != nil || !info.Mode().IsRegular() {
			in.Close()
			return errors.New("install: component must contain regular files")
		}
		destination := filepath.Join(tmp, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			in.Close()
			return err
		}
		mode := os.FileMode(0600)
		if expectedFile.Executable {
			mode = 0700
		}
		out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			in.Close()
			return err
		}
		h := sha256.New()
		_, err = io.Copy(io.MultiWriter(out, h), nativeCopyReader{ctx: ctx, Reader: in})
		in.Close()
		if err == nil {
			err = out.Sync()
		}
		closeErr := out.Close()
		if err != nil {
			return fmt.Errorf("install: component copy failed: %w", err)
		}
		if closeErr != nil {
			return fmt.Errorf("install: component write failed: %w", closeErr)
		}
		if hex.EncodeToString(h.Sum(nil)) != expectedFile.SHA256 {
			return errors.New("install: component checksum failed")
		}
	}
	raw, _ := json.Marshal(expected)
	if err = os.WriteFile(filepath.Join(tmp, ".oac-install.json"), raw, 0600); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func selectedNativeHarnesses(value string) ([]string, error) {
	var out []string
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		spec, ok := nativeHarnesses[name]
		if !ok {
			return nil, errors.New("install: --harness requires codex,claude,minimax (comma-separated)")
		}
		if !spec.Supported() {
			return nil, fmt.Errorf("install: %s adapter is unsupported on %s; select a supported Harness", name, runtime.GOOS)
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}

type nativeCopyReader struct {
	io.Reader
	ctx context.Context
}

func (r nativeCopyReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
