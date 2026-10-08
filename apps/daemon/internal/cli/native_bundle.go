package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// This is release content, never an input file for installation options.
type nativeBundle struct {
	Schema        int    `json:"schema"`
	DaemonVersion string `json:"daemon_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
}

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
func readNativeBundle(directory string) (nativeBundle, error) {
	var b nativeBundle
	if readNativeJSON(filepath.Join(directory, "bundle.json"), &b) != nil {
		return b, errors.New("install: use the native installer distribution containing bundle.json")
	}
	if b.Schema != 1 || b.DaemonVersion != Version || b.OS != runtime.GOOS || b.Arch != runtime.GOARCH {
		return b, errors.New("install: distribution version or platform mismatch; use a matching current release")
	}
	return b, nil
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
