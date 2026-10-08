//go:build linux

package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/daemonize"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
)

// nativeBundlePrograms are the distribution's programs besides oac-daemon.
var nativeBundlePrograms = []string{"oac-sandbox-io"}

const sandboxBootstrapFile = "sandbox-io-bootstrap.json"

// sandboxRestartDelay is the first and the longest wait before an exited
// oac-sandbox-io is replaced.
var sandboxRestartDelay = [2]time.Duration{time.Second, 30 * time.Second}

// sandboxStopGrace covers oac-sandbox-io's SIGTERM shutdown, at most its cancel
// grace limit plus five seconds (docs/sandbox-bootstrap.md).
const sandboxStopGrace = 40 * time.Second

// runSandboxLauncher is the Sandbox Provider for this machine's enrollment. It
// enrolls, hands oac-sandbox-io its bootstrap file and replaces the process
// whenever it exits, enrolling again first because a rotation advances the
// generation. Serve reconnects within one process, so this is the only restart
// loop; a native installation has no other supervisor.
func runSandboxLauncher(parent context.Context, rc *runContext, background bool, root string, c nativeInstallation) error {
	base, err := environmentBase(c.Remote)
	if err != nil {
		return err
	}
	keyID := ""
	// The -b parent reports a rejection to its terminal; the process that owns
	// the service parks instead.
	parks := !background || daemonize.IsBackgroundChild()
	rejected := func(err error) error {
		if message := environmentRejection(err, keyID, c.Environment); parks && message != "" {
			return parkEnvironment(parent, rc.stderr, message)
		}
		return err
	}
	// Each enrollment reads the credential file, so a replaced credential
	// takes effect without a restart.
	enroll := func() (sandboxbootstrap.Input, error) {
		id, token, err := executorCredential(c.Credential, c.Environment)
		if err != nil {
			return sandboxbootstrap.Input{}, err
		}
		keyID = id
		ctx, cancel := context.WithTimeout(parent, bootstrapTimeout)
		defer cancel()
		return enrollSandbox(ctx, environmentClient(), base, c.Environment, token)
	}
	input, err := enroll()
	if err != nil {
		return rejected(err)
	}
	if err = parent.Err(); err != nil {
		return err
	}
	if background && !daemonize.IsBackgroundChild() {
		return spawnBackground(parent, rc, paths.DefaultProfile, os.Args)
	}
	dir := filepath.Join(root, "daemon")
	held, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer held.Close()
	program := filepath.Join(root, "bin", "oac-sandbox-io")
	delay := sandboxRestartDelay[0]
	for {
		started := time.Now()
		err = runSandboxIO(parent, rc, held, program, filepath.Join(dir, sandboxBootstrapFile), input)
		if parent.Err() != nil {
			return nil
		}
		if time.Since(started) >= sandboxRestartDelay[1] {
			delay = sandboxRestartDelay[0]
		}
		fmt.Fprintf(rc.stderr, "oac-daemon: oac-sandbox-io stopped (%v); enrolling again\n", err)
		for {
			select {
			case <-parent.Done():
				return nil
			case <-time.After(delay):
			}
			delay = min(2*delay, sandboxRestartDelay[1])
			if input, err = enroll(); err == nil {
				break
			}
			if environmentRejection(err, keyID, c.Environment) != "" {
				return rejected(err)
			}
			fmt.Fprintf(rc.stderr, "oac-daemon: %v; retrying\n", err)
		}
	}
}

// enrollSandbox enrolls this machine and returns the bootstrap input of its
// enrollment resource, which the executor token serves.
func enrollSandbox(ctx context.Context, client *http.Client, base, environment, credential string) (sandboxbootstrap.Input, error) {
	raw, err := requestEnrollment(ctx, client, base, environment, credential)
	if err != nil {
		return sandboxbootstrap.Input{}, err
	}
	var out struct {
		LinkURL  string                    `json:"link_url"`
		Resource sandboxbootstrap.Resource `json:"resource"`
	}
	in := sandboxbootstrap.Input{Version: sandboxbootstrap.Version, Credential: credential}
	if decodeEnvironmentJSON(raw, &out) == nil {
		in.LinkURL, in.Resource = out.LinkURL, out.Resource
		if in.Validate() == nil && in.Resource.Kind == "enrollment" && in.Resource.EnvironmentID == environment {
			return in, nil
		}
	}
	return sandboxbootstrap.Input{}, errors.New("connect: invalid Environment enrollment response")
}

// runSandboxIO writes the bootstrap file and runs oac-sandbox-io until it
// exits. Cancelling ctx sends it SIGTERM and waits for its shutdown.
func runSandboxIO(ctx context.Context, rc *runContext, held *os.Root, program, bootstrap string, input sandboxbootstrap.Input) error {
	raw, err := input.Marshal()
	if err != nil {
		return err
	}
	if err = runtimefs.WritePrivateAtomic(held, sandboxBootstrapFile, raw); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, program, "--bootstrap-file", bootstrap)
	cmd.Stdout, cmd.Stderr = rc.stderr, rc.stderr
	// The service must not keep serving this machine after its launcher dies.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = sandboxStopGrace
	return cmd.Run()
}
