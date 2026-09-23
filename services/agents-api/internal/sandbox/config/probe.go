package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
)

// Runtime checks are cached by immutable configuration. Availability checks run
// on each heartbeat; lifecycle calls still verify the exact artifact themselves.
func microsandboxProbe(entry Microsandbox) func(context.Context) error {
	var once sync.Once
	var integrityErr error
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if runtime.GOOS != "linux" {
			return errors.New("microsandbox requires a Linux KVM node")
		}
		once.Do(func() {
			for _, artifact := range []struct{ path, hash string }{{entry.RuntimePath, entry.RuntimeSHA256}, {entry.FirmwarePath, entry.FirmwareSHA256}} {
				f, err := os.Open(artifact.path)
				if err != nil {
					integrityErr = errors.New("pinned microsandbox artifacts are unavailable")
					return
				}
				h := sha256.New()
				_, err = io.Copy(h, f)
				_ = f.Close()
				if err != nil || hex.EncodeToString(h.Sum(nil)) != artifact.hash {
					integrityErr = errors.New("microsandbox artifact integrity check failed")
					return
				}
			}
		})
		if integrityErr != nil {
			return integrityErr
		}
		helper, err := os.Stat(entry.HelperPath)
		if err != nil || !helper.Mode().IsRegular() || helper.Mode().Perm()&0111 == 0 {
			return errors.New("microsandbox helper is unavailable")
		}
		home, err := os.Lstat(entry.RuntimeHome)
		if err != nil || !home.IsDir() || home.Mode().Perm() != 0700 {
			return errors.New("microsandbox state directory is unavailable")
		}
		kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
		if err != nil {
			return errors.New("KVM is unavailable to sandbox node")
		}
		return kvm.Close()
	}
}
