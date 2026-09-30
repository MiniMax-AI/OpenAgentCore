package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
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

// Image availability is checked on every retained-generation probe, after the
// platform, capacity and local artifact checks.
func microsandboxGenerationProbe(entry Microsandbox, probe func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := probe(ctx); err != nil {
			return err
		}
		command := exec.CommandContext(ctx, entry.RuntimePath, "image", "inspect", entry.Image, "--format", "json")
		command.Env = append([]string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + os.Getenv("HOME")}, "MSB_BACKEND=local", "MSB_HOME="+entry.RuntimeHome, "MSB_PATH="+entry.RuntimePath, "MSB_LIBKRUNFW_PATH="+entry.FirmwarePath)
		reader, err := command.StdoutPipe()
		if err != nil {
			return sandbox.ErrRuntimeImageUnavailable
		}
		if err := command.Start(); err != nil {
			return sandbox.ErrRuntimeImageUnavailable
		}
		raw, readErr := io.ReadAll(io.LimitReader(reader, 64*1024+1))
		if len(raw) > 64*1024 {
			_ = command.Process.Kill()
		}
		waitErr := command.Wait()
		var image struct{ Digest, Architecture, OS string }
		if readErr != nil || waitErr != nil || len(raw) > 64*1024 || json.Unmarshal(raw, &image) != nil || image.Digest != strings.SplitN(entry.Image, "@", 2)[1] || image.Architecture != "amd64" || image.OS != "linux" {
			return sandbox.ErrRuntimeImageUnavailable
		}
		return nil
	}
}
