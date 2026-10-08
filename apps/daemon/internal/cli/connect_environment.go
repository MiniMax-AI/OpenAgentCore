package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/daemonize"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
	"github.com/google/uuid"
)

// bootstrapTimeout bounds each enrollment request.
const bootstrapTimeout = 10 * time.Second

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

func readEnvironmentPrivateFile(path string) ([]byte, error) {
	return runtimefs.ReadPrivatePath(path, 16*1024)
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

// requestEnrollment returns the body of a successful enrollment; each caller
// decodes the response it expects.
func requestEnrollment(ctx context.Context, client *http.Client, base, environment, credential string) ([]byte, error) {
	body, _ := json.Marshal(map[string]string{"environment_id": environment})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/agent-daemon/enroll", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("connect: invalid enrollment request")
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("connect: Environment enrollment transport failed")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, errEnvironmentCredentialRejected
	case http.StatusConflict:
		return nil, errEnvironmentBindingConflict
	default:
		return nil, fmt.Errorf("connect: Environment enrollment rejected (HTTP %d)", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024+1))
	if err != nil || len(raw) > 16*1024 {
		return nil, errors.New("connect: invalid Environment enrollment response")
	}
	return raw, nil
}

var (
	errEnvironmentCredentialRejected = errors.New("connect: Environment enrollment rejected (HTTP 401)")
	errEnvironmentBindingConflict    = errors.New("connect: Environment enrollment rejected (HTTP 409)")
)

// environmentRejection names the fix for a permanent enrollment rejection,
// 401 or 409. It returns "" for anything else (transport failures, 5xx, 404),
// which keeps the ordinary failure exit so the Runtime's restart policy
// retries it.
func environmentRejection(err error, keyID, environment string) string {
	reconnect, remove := "install it for this Runtime and restart it", "stop this Runtime"
	switch {
	case errors.Is(err, errEnvironmentBindingConflict):
		return fmt.Sprintf("executor credential %s cannot connect: Environment %s is bound to a different executor credential. This Runtime will not retry. Rotate the credential first used for this Environment instead of issuing a new one, then %s. To remove this Runtime instead, %s.", keyID, environment, reconnect, remove)
	case errors.Is(err, errEnvironmentCredentialRejected):
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
