package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/auth"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/daemonize"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/transport"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/google/uuid"
)

type environmentEnrollment struct {
	DeviceID           string `json:"device_id"`
	SessionID          string `json:"session_id"`
	EnvironmentID      string `json:"environment_id"`
	WorkspaceDirectory string `json:"workspace_directory"`
}

func environmentClient() *http.Client {
	return &http.Client{Timeout: bootstrapTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func environmentBase(remote string) (string, error) {
	u, err := url.Parse(remote)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Path != "/api/v1/agent-daemon/ws" || strings.TrimSpace(remote) != remote {
		return "", errors.New("connect: invalid Environment remote_url")
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("connect: Environment remote_url requires TLS outside loopback")
		}
		u.Scheme = "http"
	default:
		return "", errors.New("connect: Environment remote_url must use ws or wss")
	}
	u.Path = "/api/v1"
	return u.String(), nil
}

func environmentUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

// executorCredential returns the credential's key ID and secret token.
func executorCredential(path, environment string) (string, string, error) {
	raw, err := readEnvironmentPrivateFile(path)
	if err != nil {
		return "", "", errors.New("connect: executor credential must be a protected owned JSON file")
	}
	var key struct {
		KeyID         string `json:"key_id"`
		Token         string `json:"executor_token"`
		EnvironmentID string `json:"environment_id,omitempty"`
	}
	if decodeEnvironmentJSON(raw, &key) != nil || !environmentUUID(key.KeyID) || key.Token == "" || strings.ContainsAny(key.Token, " \t\r\n\x00") || (key.EnvironmentID != "" && key.EnvironmentID != environment) {
		return "", "", errors.New("connect: invalid executor credential or Environment restriction")
	}
	return key.KeyID, key.Token, nil
}

func decodeEnvironmentJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func enrollEnvironment(ctx context.Context, client *http.Client, base, environment, credential string) (environmentEnrollment, error) {
	var out environmentEnrollment
	body, _ := json.Marshal(map[string]string{"environment_id": environment})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/agent-daemon/enroll", bytes.NewReader(body))
	if err != nil {
		return out, errors.New("connect: invalid enrollment request")
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return out, errors.New("connect: Environment enrollment transport failed")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return out, errEnvironmentCredentialRejected
	case http.StatusConflict:
		return out, errEnvironmentBindingConflict
	default:
		return out, fmt.Errorf("connect: Environment enrollment rejected (HTTP %d)", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024+1))
	if err != nil || len(raw) > 16*1024 || decodeEnvironmentJSON(raw, &out) != nil || !environmentUUID(out.DeviceID) || !environmentUUID(out.SessionID) || out.EnvironmentID != environment || out.WorkspaceDirectory == "/" || agentcapabilities.ValidateLocalDirectories([]string{out.WorkspaceDirectory}) != nil {
		return environmentEnrollment{}, errors.New("connect: invalid Environment enrollment response")
	}
	return out, nil
}

func environmentBootstrap(ctx context.Context, prof auth.Profile, remote string) (*transport.BootstrapResponse, error) {
	boot, err := transport.BootstrapWithClient(ctx, environmentClient(), prof.ServerURL, prof.RuntimeID, prof.RunnerCredential, Version)
	if err != nil {
		return nil, errors.New("connect: Environment bootstrap failed")
	}
	if boot.DeviceID != prof.RuntimeID || boot.WSURL != remote {
		return nil, errors.New("connect: Environment bootstrap changed the bound device or remote_url")
	}
	return boot, nil
}

func runEnvironmentConnect(parent context.Context, rc *runContext, profile string, background bool, remote, environment, credentialFile string) error {
	base, err := environmentBase(remote)
	if err != nil {
		return err
	}
	if !environmentUUID(environment) {
		return errors.New("connect: canonical Environment ID required")
	}
	if err = checkEnvironmentTarget(remote, environment); err != nil {
		return err
	}
	keyID, credential, err := executorCredential(credentialFile, environment)
	if err != nil {
		return err
	}
	// The -b parent reports a rejection to its terminal; the process that owns
	// the connection parks instead.
	parks := !background || daemonize.IsBackgroundChild()
	rejected := func(err error) error {
		if message := environmentRejection(err, keyID, environment); parks && message != "" {
			return parkEnvironment(parent, rc.stderr, message)
		}
		return err
	}
	ctx, cancel := context.WithTimeout(parent, bootstrapTimeout)
	defer cancel()
	bound, err := enrollEnvironment(ctx, environmentClient(), base, environment, credential)
	if err != nil {
		return rejected(err)
	}
	if err = parent.Err(); err != nil {
		return err
	}
	if err = bindEnvironmentRuntime(remote, bound, credentialFile); err != nil {
		return err
	}
	if background && !daemonize.IsBackgroundChild() {
		return spawnBackground(parent, rc, profile, os.Args, nil)
	}
	// Discovery consumes the immutable Runtime binding; it must follow enrollment.
	discovery, err := preflightAgentCLIs(parent, rc, profile)
	if err != nil {
		return err
	}
	prof := auth.Profile{ServerURL: base, RuntimeID: bound.DeviceID, RunnerCredential: credential}
	return rejected(mainLoopRemote(parent, rc, profile, prof, discovery, remote))
}

var (
	errEnvironmentCredentialRejected = errors.New("connect: Environment enrollment rejected (HTTP 401)")
	errEnvironmentBindingConflict    = errors.New("connect: Environment enrollment rejected (HTTP 409)")
)

// environmentRejection names the fix for a permanent Environment rejection:
// enrollment 401 or 409, or a permanent WebSocket rejection or close. It returns
// "" for anything else (transport failures, 5xx, 404), which keeps the ordinary
// failure exit so the Runtime's restart policy retries it.
func environmentRejection(err error, keyID, environment string) string {
	reconnect, remove := "install it for this Runtime and restart it", "stop this Runtime"
	switch {
	case errors.Is(err, errEnvironmentBindingConflict):
		return fmt.Sprintf("executor credential %s cannot connect: Environment %s is bound to a different executor credential. This Runtime will not retry. Rotate the credential first used for this Environment instead of issuing a new one, then %s. To remove this Runtime instead, %s.", keyID, environment, reconnect, remove)
	case errors.Is(err, transport.ErrIncompatibleVersion):
		return fmt.Sprintf("Core refused this Runtime's daemon version for Environment %s; the Runtime comes from a different Core distribution. This Runtime will not retry; %s.", environment, remove)
	case errors.Is(err, errEnvironmentCredentialRejected), errors.Is(err, transport.ErrPermanent):
		return fmt.Sprintf("executor credential %s for Environment %s was rejected by Core (revoked, rotated, or its Session was deleted). This Runtime will not retry. To reconnect it, rotate this credential in Web (Session > Executor credentials > Rotate), then %s. To remove it instead, %s.", keyID, environment, reconnect, remove)
	}
	return ""
}

// parkEnvironment prints the rejection once, then makes no further requests
// until SIGINT or SIGTERM and exits successfully. Docker's unless-stopped policy
// restarts every exit, so an exit would loop; a parked Runtime still restarts
// after a reboot, makes one enrollment request and parks again.
func parkEnvironment(parent context.Context, stderr io.Writer, message string) error {
	ctx, stop := daemonize.NotifyContext(parent)
	defer stop()
	fmt.Fprintln(stderr, "oac-daemon: "+message)
	<-ctx.Done()
	return nil
}
