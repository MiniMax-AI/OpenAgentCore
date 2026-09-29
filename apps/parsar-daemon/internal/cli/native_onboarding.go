package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/daemonize"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/runtimefs"
)

func installationRequest(ctx context.Context, endpoint, authorization string, input any, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("install: invalid Core installation endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+authorization)
	req.Header.Set("Content-Type", "application/json")
	response, err := environmentClient().Do(req)
	if err != nil {
		return errors.New("install: could not reach Core; check its address and retry")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK, http.StatusNoContent:
	case http.StatusUnauthorized:
		return errors.New("install: authorization rejected or expired; copy a fresh command from the Session")
	case http.StatusConflict:
		return errors.New("install: this Environment already has a different or revoked credential; reuse the original installation or resolve its credential in Core")
	default:
		return fmt.Errorf("install: Core rejected installation (HTTP %d); check the Session and obtain a fresh command", response.StatusCode)
	}
	if output == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(raw) > 16384 || decodeEnvironmentJSON(raw, output) != nil {
		return errors.New("install: invalid Core installation response")
	}
	return nil
}

func prepareOnboarding(o *nativeInstallOptions) error {
	if o.OnboardURL == "" && o.Authorization == "" {
		return nil
	}
	if o.OnboardURL == "" || o.Authorization == "" {
		return errors.New("install: --onboard-url and --authorization are required together")
	}
	const suffix = "/api/v1/agent-daemon/installation"
	if !strings.HasSuffix(o.OnboardURL, suffix) {
		return errors.New("install: invalid Core installation endpoint")
	}
	remote := strings.TrimSuffix(o.OnboardURL, suffix) + "/api/v1/agent-daemon/ws"
	remote = strings.Replace(strings.Replace(remote, "https://", "wss://", 1), "http://", "ws://", 1)
	if _, err := environmentBase(remote); err != nil {
		return err
	}
	var info v1.NativeInstallationContext
	if err := installationRequest(context.Background(), o.OnboardURL, o.Authorization, nil, &info); err != nil {
		return err
	}
	if info.Version != Version || !proto.VersionCompatible(info.ProtocolVersion) {
		return errors.New("install: installer does not match Core; obtain a fresh command from the target Core")
	}
	if info.RemoteURL != remote || !environmentUUID(info.EnvironmentID) {
		return errors.New("install: Core returned an invalid connection binding")
	}
	if (o.Remote != "" && o.Remote != info.RemoteURL) || (o.Environment != "" && o.Environment != info.EnvironmentID) || (o.Workspace != "" && o.Workspace != info.Workspace) || o.Credential != "" {
		return errors.New("install: supplied options conflict with the Session's frozen environment")
	}
	o.Remote, o.Environment, o.Workspace = info.RemoteURL, info.EnvironmentID, info.Workspace
	for name, spec := range nativeHarnesses {
		if spec.AgentKind == info.Harness {
			o.RequiredHarness = name
		}
	}
	if o.RequiredHarness == "" {
		return errors.New("install: the Session requires an unsupported Harness")
	}
	if o.Directory == "" {
		root, err := paths.Root()
		if err != nil {
			return err
		}
		o.Directory = filepath.Join(root, "environments", o.Environment)
	}
	if !o.NonInteractive {
		o.Interactive = true
	}
	return nil
}

// prepareOnboardingCredential runs under the existing installation lock. The
// credential is committed locally before claiming it so any response can be lost
// without losing the only usable secret. No authorization is retained on disk.
func prepareOnboardingCredential(ctx context.Context, o *nativeInstallOptions, held *os.Root) error {
	var previous nativeInstallation
	if raw, err := runtimefs.ReadPrivate(held, "installation.json", 1<<20); err == nil {
		if decodeEnvironmentJSON(raw, &previous) != nil || previous.Version != Version || previous.Remote != o.Remote || previous.Environment != o.Environment || previous.Workspace != o.Workspace {
			return errors.New("install: this directory belongs to an incompatible installation; choose a separate directory")
		}
		o.Credential = previous.Credential
		// A completed installation already owns its key. Rotation and revocation
		// are checked by normal enrollment, never undone by the installer.
		_, _, err := executorCredential(o.Credential, o.Environment)
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	o.Credential = filepath.Join(o.Directory, "daemon", "executor-credential.json")
	var secret string
	if _, err := held.Stat("executor-credential.json"); err == nil {
		keyID, value, err := executorCredential(o.Credential, o.Environment)
		if err != nil || keyID != o.Environment {
			return errors.New("install: existing credential belongs to a different Environment")
		}
		secret = value
	} else if errors.Is(err, os.ErrNotExist) {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		secret = base64.RawURLEncoding.EncodeToString(raw)
		encoded, _ := json.Marshal(map[string]string{"key_id": o.Environment, "environment_id": o.Environment, "executor_token": secret})
		if err := runtimefs.WritePrivateAtomic(held, "executor-credential.json", encoded); err != nil {
			return err
		}
	} else {
		return err
	}
	return installationRequest(ctx, o.OnboardURL+"/claim", o.Authorization, map[string]string{"executor_token": secret}, nil)
}

func finishOnboarding(ctx context.Context, rc *runContext, o nativeInstallOptions) error {
	executable := filepath.Join(o.Directory, "bin", nativeExe("oac-daemon"))
	pidPath := filepath.Join(o.Directory, "daemon", paths.DefaultProfile, "connect.pid")
	if _, err := daemonize.ReadPIDFile(pidPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, daemonize.ErrStaleOrCorrupt) {
			return err
		}
		// Run the installed executable. A detached child must never reference the
		// temporary bundle or inherit the short-lived authorization in argv.
		command := exec.CommandContext(ctx, executable, "start")
		command.Env = withNativeEnv(map[string]string{"OAC_RUNTIME_HOME": o.Directory, daemonize.BackgroundSentinelEnv: ""})
		command.Stdout, command.Stderr = rc.stdout, rc.stderr
		if err := command.Run(); err != nil {
			fmt.Fprintln(rc.stderr, "Daemon connection: start failed. Rerun this command to resume, or run the installed oac-daemon start.")
			return errors.New("install: daemon startup failed")
		}
	}
	_, secret, err := executorCredential(o.Credential, o.Environment)
	if err != nil {
		return err
	}
	base, _ := environmentBase(o.Remote)
	deadline, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		connected, err := installedEnvironmentConnected(deadline, base, o.Environment, secret)
		if err != nil {
			return err
		}
		if connected {
			fmt.Fprintln(rc.stdout, "Daemon connection: connected to Core.")
			return nil
		}
		select {
		case <-deadline.Done():
			fmt.Fprintf(rc.stderr, "Daemon connection: not confirmed. The installed daemon will keep reconnecting. Check %s and Core connectivity, then rerun this command.\n", filepath.Join(o.Directory, "daemon", paths.DefaultProfile, "connect.log"))
			return errors.New("install: connection verification timed out")
		case <-ticker.C:
		}
	}
}

func installedEnvironmentConnected(ctx context.Context, base, environment, secret string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/agent-daemon/connection?environment_id="+environment, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	response, err := environmentClient().Do(req)
	if err != nil {
		return false, nil
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 409 {
		return false, errors.New("Daemon connection: credential rejected; check the Environment credential in Core")
	}
	if response.StatusCode != http.StatusOK {
		return false, nil
	}
	var result struct {
		Environment string `json:"environment_id"`
		Status      string `json:"status"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result) != nil || result.Environment != environment {
		return false, errors.New("Daemon connection: invalid status returned by Core")
	}
	return result.Status == "connected", nil
}
