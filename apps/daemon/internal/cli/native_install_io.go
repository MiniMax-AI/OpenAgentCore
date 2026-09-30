package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// These exact temporary names are reserved by the installer, never workspace data.
var nativeTemporaryNames = map[string]*regexp.Regexp{
	"components": regexp.MustCompile(`^\.install-(node|codex|claude|minimax)-[0-9]+$`),
	"bin":        regexp.MustCompile(`^\.oac-daemon-[0-9]+$`),
	"daemon":     regexp.MustCompile(`^\.(installation\.json|executor-credential\.json)-[0-9a-f]{24}\.tmp$`),
}

// The caller holds the installation lock, including while recovering a failed copy.
func cleanNativeTemporaryFiles(root string) error {
	for directory, pattern := range nativeTemporaryNames {
		parent := filepath.Join(root, directory)
		info, err := os.Lstat(parent)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("install: %s must be a directory, not a link", directory)
		}
		entries, err := os.ReadDir(parent)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !pattern.MatchString(entry.Name()) {
				continue
			}
			// RemoveAll unlinks a link itself and never follows its target.
			if err = os.RemoveAll(filepath.Join(parent, entry.Name())); err != nil {
				return fmt.Errorf("install: cannot remove interrupted staging: %w", err)
			}
		}
	}
	return nil
}

func requireNativeSpace(directory string, size uint64) error {
	available, err := nativeAvailableSpace(directory)
	if err != nil {
		return fmt.Errorf("install: cannot check free space: %w", err)
	}
	const reserve = 64 << 20
	if available < reserve || size > available-reserve {
		return errors.New("install: not enough disk space; free space in the installation directory and retry")
	}
	return nil
}

func nativeInstallError(err error) error {
	if err == nil {
		return nil
	}
	if nativeDiskFull(err) {
		return errors.New("install: disk space or quota exhausted; free space and retry (completed components and credentials were preserved)")
	}
	if errors.Is(err, os.ErrPermission) {
		return errors.New("install: filesystem access denied; check directory permissions and files in use, then retry with the same account")
	}
	return err
}

// Non-terminal output contains ordinary phase lines, with no redraws or escapes.
func nativeInstallPhase(output io.Writer, label string, operation func() error) error {
	fmt.Fprintln(output, label+"...")
	file, ok := output.(*os.File)
	if !ok || !nativeTerminal(file) {
		return operation()
	}
	done, stopped := make(chan struct{}), make(chan struct{})
	start := time.Now()
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Fprintf(output, "\r%s... %ds", label, int(time.Since(start).Seconds()))
			}
		}
	}()
	defer func() { close(done); <-stopped; fmt.Fprintln(output) }()
	return operation()
}

func nativeCopy(ctx context.Context, out io.Writer, in io.Reader) (int64, error) {
	return io.Copy(out, nativeCopyReader{ctx: ctx, Reader: in})
}
