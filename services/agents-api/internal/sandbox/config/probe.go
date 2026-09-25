package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

// Probes report the first failed check. Precedence runs from the provider
// platform (Docker daemon or KVM), through Docker limit support and host
// capacity for one sandbox of the deployment specification, to the installed
// Runtime content (image or microsandbox artifacts). Unclassified failures stay
// provider_unavailable. The returned text is local; only its code is reported.

// kvmDevice is replaceable only by tests.
var kvmDevice = "/dev/kvm"

func dockerProbe(c *client.Client, image string, resources sandbox.Resources) func(context.Context) error {
	return func(ctx context.Context) error {
		if _, err := c.Ping(ctx, client.PingOptions{}); err != nil {
			return sandbox.ErrDockerUnavailable
		}
		host, err := c.Info(ctx, client.InfoOptions{})
		if err != nil {
			return fmt.Errorf("%w: cannot inspect Docker host resource support", sandbox.ErrDockerUnavailable)
		}
		if !host.Info.MemoryLimit || !host.Info.CPUCfsQuota {
			return sandbox.ErrDockerLimitsUnsupported
		}
		if host.Info.MemTotal <= 0 {
			return errors.New("Docker host memory capacity is unavailable")
		}
		if err := checkCapacity(resources, host.Info.NCPU, uint64(host.Info.MemTotal)); err != nil {
			return err
		}
		if _, err = c.ImageInspect(ctx, image); errdefs.IsNotFound(err) {
			return sandbox.ErrRuntimeImageUnavailable
		} else if err != nil {
			// The daemon did not answer; the image may still be present.
			return fmt.Errorf("%w: cannot inspect the pinned Runtime image", sandbox.ErrDockerUnavailable)
		}
		return nil
	}
}

// A successful Runtime integrity check is cached for this immutable
// configuration. A failure is checked again on the next heartbeat, so repaired
// artifacts recover without a restart. Lifecycle calls still verify the exact
// artifact themselves.
func microsandboxProbe(entry Microsandbox, resources sandbox.Resources) func(context.Context) error {
	var integrity sync.Mutex
	verified := false
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if runtime.GOOS != "linux" {
			return fmt.Errorf("%w: microsandbox requires a Linux KVM node", sandbox.ErrKVMUnavailable)
		}
		kvm, err := os.OpenFile(kvmDevice, os.O_RDWR, 0)
		if err != nil {
			return sandbox.ErrKVMUnavailable
		}
		_ = kvm.Close()
		if err := hostCapacity(resources); err != nil {
			return err
		}
		integrity.Lock()
		if !verified {
			if err := verifyMicrosandboxArtifacts(entry); err != nil {
				integrity.Unlock()
				return err
			}
			verified = true
		}
		integrity.Unlock()
		helper, err := os.Stat(entry.HelperPath)
		if err != nil || !helper.Mode().IsRegular() || helper.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("%w: microsandbox helper is unavailable", sandbox.ErrMicrosandboxArtifactsUnavailable)
		}
		home, err := os.Lstat(entry.RuntimeHome)
		if err != nil || !home.IsDir() || home.Mode().Perm() != 0700 {
			return errors.New("microsandbox state directory is unavailable")
		}
		return nil
	}
}

func verifyMicrosandboxArtifacts(entry Microsandbox) error {
	for _, artifact := range []struct{ path, hash string }{{entry.RuntimePath, entry.RuntimeSHA256}, {entry.FirmwarePath, entry.FirmwareSHA256}} {
		f, err := os.Open(artifact.path)
		if err != nil {
			return sandbox.ErrMicrosandboxArtifactsUnavailable
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil || hex.EncodeToString(h.Sum(nil)) != artifact.hash {
			return fmt.Errorf("%w: artifact integrity check failed", sandbox.ErrMicrosandboxArtifactsUnavailable)
		}
	}
	return nil
}
