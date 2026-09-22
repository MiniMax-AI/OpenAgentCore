package cubesandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

// Paths and modes that belong to the shared Runtime contract. They are byte
// compatible with the Docker adapter, because the daemon side is unchanged.
const (
	homeDir     = "/home/runtime"
	profileDir  = homeDir + "/.parsar/parsar-daemon/default"
	profilePath = profileDir + "/auth.json"
	// privateDirs is created before the credential exists, so the profile is
	// never briefly readable inside a wide-open directory.
	privateDirs     = homeDir + "/.parsar " + homeDir + "/.parsar/parsar-daemon " + profileDir
	environmentDirs = environmentMount + "/workspace " + environmentMount + "/staging " + environmentMount + "/initialization " + environmentMount + "/packages"
	// bootstrapStepTimeout bounds one mutating bootstrap operation.
	bootstrapStepTimeout = 2 * time.Minute
)

// bootstrap writes the daemon auth profile and prepares the environment layout
// before the harness can run. It never places the credential in argv, an
// environment variable, metadata or an image layer: the bytes travel in an envd
// file request, and only the resulting mode is verified by a command.
func (p *Provider) bootstrap(ctx context.Context, observed cubeSandbox, b sandbox.Bootstrap) error {
	auth, err := json.Marshal(struct {
		ServerURL  string `json:"server_url"`
		RuntimeID  string `json:"runtime_id"`
		Credential string `json:"runner_credential"`
	}{b.CoreURL, b.DeviceID, b.Credential})
	if err != nil {
		return err
	}
	if err := p.trusted(ctx, observed, "/bin/sh", "-c", "umask 077 && mkdir -p "+privateDirs+" && chmod 700 "+privateDirs+" && mkdir -p "+environmentDirs+" && chmod 700 "+environmentDirs); err != nil {
		return err
	}
	if err := p.writeFile(ctx, observed, profilePath, auth); err != nil {
		return err
	}
	// envd's file API carries no mode, so the mode and ownership are applied and
	// verified here. An unverified profile is not a profile this adapter admits.
	verify := fmt.Sprintf("chmod 600 %[1]s && test \"$(stat -c %%a %[1]s)\" = 600 && test \"$(stat -c %%u %[1]s)\" = 1000 && test \"$(stat -c %%g %[1]s)\" = 1000", profilePath)
	return p.trusted(ctx, observed, "/bin/sh", "-c", verify)
}

// trusted runs one bounded bootstrap command as the Runtime user and requires a
// zero exit status. A non-zero status is a real failure; an unconfirmed stream is
// reported as unconfirmed so the caller reclaims the allocation.
func (p *Provider) trusted(ctx context.Context, observed cubeSandbox, args ...string) error {
	step, cancel := context.WithTimeout(ctx, bootstrapStepTimeout)
	defer cancel()
	result, err := p.run(step, observed, runtimeUser, sandbox.Command{Args: args})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return errors.New("CubeSandbox Runtime bootstrap command failed")
	}
	return nil
}
