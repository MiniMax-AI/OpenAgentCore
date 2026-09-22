package cubesandbox

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

func connectPayload(t *testing.T, body []byte) map[string]any {
	t.Helper()
	if len(body) < 5 {
		t.Fatalf("Connect envelope framing missing: %q", body)
	}
	var payload map[string]any
	if json.Unmarshal(body[5:], &payload) != nil {
		t.Fatalf("Connect payload was not JSON: %q", body[5:])
	}
	return payload
}

func commandFixture(t *testing.T) (*cluster, *Provider, sandbox.Reference) {
	t.Helper()
	cluster := newCluster(t)
	installation := uuid.NewString()
	provider := cluster.provider(installation)
	bootstrap := cluster.bootstrap()
	id := cluster.addSandbox(cluster.metadata(installation, bootstrap.Reference), "running")
	if id == "" {
		t.Fatal("sandbox was not seeded")
	}
	return cluster, provider, bootstrap.Reference
}

// A non-zero exit status is a result, not a transport failure, and both streams
// are decoded from the pinned Base64 encoding.
func TestRunCommandPreservesExitStatusAndStreams(t *testing.T) {
	cluster, provider, reference := commandFixture(t)
	cluster.stream = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", connectContentType)
		w.WriteHeader(http.StatusOK)
		sendEvent(w, map[string]any{"start": map[string]any{"pid": 77}})
		sendEvent(w, map[string]any{"data": map[string]any{"stdout": encode("receipt"), "stderr": encode("warning")}})
		sendEvent(w, map[string]any{"end": map[string]any{"exitCode": 7, "exited": true, "status": "exit status 7"}})
		sendEndStream(w, "")
	}
	result, err := provider.RunCommand(testContext(t), reference, sandbox.Command{Args: []string{"/usr/bin/python3", "/opt/receipt.py"}, Directory: "/environment/initialization"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || result.Stdout != "receipt" || result.Stderr != "warning" {
		t.Fatalf("command result lost: %+v", result)
	}
	// argv, the working directory and the unprivileged user are all explicit.
	var start []byte
	for _, request := range cluster.dataRequests() {
		if request.Path == "/process.Process/Start" {
			start = request.Body
		}
	}
	payload := connectPayload(t, start)
	process, _ := payload["process"].(map[string]any)
	if process["cmd"] != "/usr/bin/python3" || process["cwd"] != "/environment/initialization" {
		t.Fatalf("process request lost argv or directory: %v", payload)
	}
	args, _ := process["args"].([]any)
	if len(args) != 1 || args[0] != "/opt/receipt.py" {
		t.Fatalf("process request lost arguments: %v", payload)
	}
	if payload["stdin"] != false {
		t.Fatal("stdin was requested without input")
	}
	auth := base64.StdEncoding.EncodeToString([]byte("runtime:"))
	for _, request := range cluster.dataRequests() {
		if request.Path == "/process.Process/Start" && request.Auth != "Basic "+auth {
			t.Fatalf("command did not run as the Runtime user: %q", request.Auth)
		}
	}
}

// stdin travels in request bodies, never in argv, and is closed explicitly.
func TestRunCommandDeliversStdinOutsideArgv(t *testing.T) {
	cluster, provider, reference := commandFixture(t)
	confidential := strings.Repeat("confidential-payload-", 64)
	result, err := provider.RunCommand(testContext(t), reference, sandbox.Command{Args: []string{"sh", "-c", "cat"}, Stdin: []byte(confidential)})
	if err != nil || result.Stdout != "" {
		t.Fatalf("stdin command failed: %+v %v", result, err)
	}
	var startPayload, sentPayload []byte
	closed := 0
	for _, request := range cluster.dataRequests() {
		switch request.Path {
		case "/process.Process/Start":
			startPayload = request.Body
			if strings.Contains(string(request.Body), confidential) {
				t.Fatal("stdin reached argv")
			}
		case "/process.Process/SendInput":
			sentPayload = request.Body
		case "/process.Process/CloseStdin":
			closed++
		}
	}
	if len(sentPayload) == 0 || !strings.Contains(string(sentPayload), encode(confidential)) {
		t.Fatalf("stdin was not delivered over the pinned input operation: %q", sentPayload)
	}
	if closed != 1 {
		t.Fatalf("stdin was not closed exactly once: %d", closed)
	}
	payload := connectPayload(t, startPayload)
	if payload["stdin"] != true {
		t.Fatal("stdin was not requested for a command with input")
	}
}

// Invalid command input never reaches the cluster, and an uncertain outcome is
// always reported as unconfirmed so the caller reclaims instead of replaying.
func TestRunCommandRejectsInvalidInputAndCapsOutput(t *testing.T) {
	cluster, provider, reference := commandFixture(t)
	ctx := testContext(t)
	for name, command := range map[string]sandbox.Command{
		"no arguments":        {},
		"empty program":       {Args: []string{""}},
		"relative directory":  {Args: []string{"ls"}, Directory: "environment"},
		"input above the cap": {Args: []string{"cat"}, Stdin: make([]byte, sandbox.MaxCommandInputBytes+1)},
	} {
		if _, err := provider.RunCommand(ctx, reference, command); !errors.Is(err, sandbox.ErrInvalid) {
			t.Fatalf("%s: invalid command accepted (%v)", name, err)
		}
	}
	if len(cluster.dataRequests()) != 0 {
		t.Fatalf("invalid command produced cluster traffic: %d", len(cluster.dataRequests()))
	}
	if _, err := provider.RunCommand(t.Context(), reference, sandbox.Command{Args: []string{"ls"}}); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatalf("a command without a deadline was accepted: %v", err)
	}

	// Output above the per-stream cap fails the call instead of truncating.
	cluster.stream = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", connectContentType)
		w.WriteHeader(http.StatusOK)
		sendEvent(w, map[string]any{"start": map[string]any{"pid": 5}})
		sendEvent(w, map[string]any{"data": map[string]any{"stdout": encode(strings.Repeat("x", commandOutputBytes+1))}})
		sendEvent(w, map[string]any{"end": map[string]any{"exitCode": 0, "exited": true, "status": "exit status 0"}})
	}
	if _, err := provider.RunCommand(ctx, reference, sandbox.Command{Args: []string{"cat", "/environment/workspace/big"}}); !errors.Is(err, sandbox.ErrCommandUnconfirmed) {
		t.Fatalf("oversized output was not reported as unconfirmed: %v", err)
	}

	// A truncated stream is unconfirmed.
	cluster.stream = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", connectContentType)
		w.WriteHeader(http.StatusOK)
		var header [5]byte
		header[1] = 0
		header[2] = 0
		header[3] = 4
		header[4] = 0
		_, _ = w.Write(header[:])
		_, _ = w.Write([]byte("ab"))
	}
	if _, err := provider.RunCommand(ctx, reference, sandbox.Command{Args: []string{"cat", "/environment/workspace/partial"}}); !errors.Is(err, sandbox.ErrCommandUnconfirmed) {
		t.Fatalf("truncated stream was not reported as unconfirmed: %v", err)
	}

	// A stream-level error frame is unconfirmed and must not be echoed.
	cluster.stream = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", connectContentType)
		w.WriteHeader(http.StatusOK)
		sendEvent(w, map[string]any{"start": map[string]any{"pid": 9}})
		sendEndStream(w, "internal")
	}
	_, err := provider.RunCommand(ctx, reference, sandbox.Command{Args: []string{"cat", "/environment/workspace/frame"}})
	if !errors.Is(err, sandbox.ErrCommandUnconfirmed) {
		t.Fatalf("stream error frame was not reported as unconfirmed: %v", err)
	}
	if strings.Contains(err.Error(), "synthetic stream failure") {
		t.Fatalf("stream error body was echoed: %v", err)
	}

	// A rejected request is certain, not uncertain; a server failure is not.
	cluster.stream = func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
	}
	if _, err := provider.RunCommand(ctx, reference, sandbox.Command{Args: []string{"cat", "/environment/workspace/rejected"}}); err == nil || errors.Is(err, sandbox.ErrCommandUnconfirmed) {
		t.Fatalf("rejected command was reported as uncertain: %v", err)
	}
	cluster.stream = func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	if _, err := provider.RunCommand(ctx, reference, sandbox.Command{Args: []string{"cat", "/environment/workspace/failed"}}); !errors.Is(err, sandbox.ErrCommandUnconfirmed) {
		t.Fatalf("server failure was reported as certain: %v", err)
	}
}

// Bootstrap writes the auth profile through the file API and verifies its mode
// and ownership with a bounded command as the Runtime user.
func TestBootstrapWritesAndVerifiesTheAuthProfile(t *testing.T) {
	cluster := newCluster(t)
	provider := cluster.provider(uuid.NewString())
	bootstrap := cluster.bootstrap()
	if _, err := provider.Create(testContext(t), bootstrap); err != nil {
		t.Fatal(err)
	}
	writes := 0
	for _, request := range cluster.dataRequests() {
		if request.Path != "/files" {
			continue
		}
		writes++
		if !strings.Contains(request.Query, "username=runtime") || !strings.Contains(request.Query, "path=%2Fhome%2Fruntime%2F.parsar%2Fparsar-daemon%2Fdefault%2Fauth.json") {
			t.Fatalf("auth profile path or user lost: %q", request.Query)
		}
		profile := map[string]string{}
		if json.Unmarshal(request.Body, &profile) != nil {
			t.Fatal("auth profile was not JSON")
		}
		if profile["server_url"] != bootstrap.CoreURL || profile["runtime_id"] != bootstrap.DeviceID || profile["runner_credential"] != bootstrap.Credential {
			t.Fatalf("auth profile contract changed: %v", profile)
		}
	}
	if writes != 1 {
		t.Fatalf("expected exactly one auth profile write, saw %d", writes)
	}
	verified := false
	for _, request := range cluster.dataRequests() {
		if request.Path != "/process.Process/Start" {
			continue
		}
		script := string(request.Body)
		if strings.Contains(script, "chmod 600") && strings.Contains(script, "stat -c %a") {
			verified = true
		}
	}
	if !verified {
		t.Fatal("the auth profile mode was never verified")
	}
}
