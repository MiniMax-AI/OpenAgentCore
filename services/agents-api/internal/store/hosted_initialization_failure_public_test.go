package store_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

const hostedFailureCanary = "CANARY-hosted-init-7c21"

// leakyReceipt is a failed receipt that also carries canary output in fields
// Core must never read, as a leaking or newer Runtime could send.
func leakyReceipt(fields string) sandbox.CommandResult {
	return sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed",` + fields + `"output":"` + hostedFailureCanary + `","stderr":"` + hostedFailureCanary + `"}`}
}

func hostedFailureSkill(t *testing.T) store.EnvironmentSkill {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.CreateHeader(&zip.FileHeader{Name: "proof/SKILL.md", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("---\nname: proof\ndescription: A proof.\n---\n" + hostedFailureCanary)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return store.EnvironmentSkill{Metadata: store.EnvironmentSkillMetadata{Type: "inline", Name: "proof", Description: "A proof."}, Archive: archive.Bytes()}
}

// hostedFailureProvider fails one initialization step with a controlled result.
// Every failure it reports is produced next to canary output, which the
// initializer discards and Core must never publish.
type hostedFailureProvider struct {
	lifecycleProvider
	fail   string // runtime-initialize action, or "file" for the initial file writer
	skip   int    // matching steps that succeed before the failure
	result sandbox.CommandResult
	err    error
	steps  []string
}

func (p *hostedFailureProvider) RunCommand(_ context.Context, _ sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	action := "file"
	if c.Args[len(c.Args)-1] == "/usr/local/bin/agents-api-runtime-initialize" {
		var operation struct {
			Action string `json:"action"`
		}
		if json.Unmarshal(c.Stdin, &operation) != nil || operation.Action == "" {
			return sandbox.CommandResult{}, sandbox.ErrInvalid
		}
		action = operation.Action
	}
	p.mu.Lock()
	p.steps = append(p.steps, action)
	p.mu.Unlock()
	if action == p.fail {
		if p.skip == 0 {
			return p.result, p.err
		}
		p.skip--
	}
	if action == "file" {
		size, err := strconv.Atoi(c.Args[len(c.Args)-1])
		if err != nil {
			return sandbox.CommandResult{}, sandbox.ErrInvalid
		}
		return sandbox.CommandResult{Stdout: fmt.Sprintf(`{"version":1,"outcome":"completed","size_bytes":%d}`, size)}, nil
	}
	return sandbox.CommandResult{Stdout: `{"version":1,"outcome":"completed"}`}, nil
}

func hostedFailureStore(t *testing.T) *store.Store {
	t.Helper()
	_, pool := store.NewManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return store.NewWithCredentialCipher(pool, cipher)
}

func hostedFailureSession(t *testing.T, s *store.Store, tenant string, input store.CreateSessionInput) (store.Session, store.Environment) {
	t.Helper()
	input.Creator, input.Engine, input.IdempotencyKey = store.FixtureCreator(), "codex", uuid.NewString()
	input.Configuration = json.RawMessage(`{"agent":{"id":"agent_test","model":"test-model","tools":[]},"environment":{"type":"openai_hosted","network":{"access":"enabled"}}}`)
	if input.Initialization.Env == nil {
		input.Initialization.Env = map[string]string{"SCAN_VALUE": hostedFailureCanary}
	}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	return session, environment
}

func failHostedInitialization(t *testing.T, s *store.Store, tenant string, environment store.Environment, p *hostedFailureProvider) {
	t.Helper()
	key := uuid.NewString()
	w, _ := managedWorker(t, s, key, p)
	if _, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, key); err != nil {
		t.Fatal(err)
	}
	reconcileManagedState(t, w, s, tenant, environment.ID, "released")
}

// H1/H2/H3/H4: one transaction records the Environment failure, an error event
// with the safe reason and agent.session.failed; reads and events agree, and a
// confirmed step names only its label and exit status.
func TestHostedInitializationFailureRecordsSafeSessionFailure(t *testing.T) {
	commands := []store.SetupCommand{{Command: "echo " + hostedFailureCanary + "; exit 0"}, {Command: "echo " + hostedFailureCanary + "; exit 3"}, {Command: "touch never"}}
	type failure struct {
		fail   string
		skip   int
		result sandbox.CommandResult
		err    error
	}
	for _, test := range []struct {
		name   string
		input  store.CreateSessionInput
		p      failure
		reason string
		steps  []string
	}{
		{"setup exit status", store.CreateSessionInput{Initialization: store.EnvironmentSetup{Commands: commands[1:]}},
			failure{fail: "setup", result: leakyReceipt(`"exit_code":3,`)},
			`Failed to provision environment: script "setup_commands[0]" failed with exit code 3`, []string{"configure", "setup"}},
		{"later setup command", store.CreateSessionInput{Initialization: store.EnvironmentSetup{Commands: commands}},
			failure{fail: "setup", skip: 1, result: sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":3}` + "\n"}},
			`Failed to provision environment: script "setup_commands[1]" failed with exit code 3`, []string{"configure", "setup", "setup"}},
		{"python package", store.CreateSessionInput{Initialization: store.EnvironmentSetup{Packages: v1.EnvironmentPackages{Python: []string{"parsar-nonexistent-zz"}}, Commands: commands[2:]}},
			failure{fail: "python", result: leakyReceipt(`"exit_code":1,`)},
			`Failed to provision environment: script "Python package installation" failed with exit code 1`, []string{"configure", "python"}},
		{"old image without exit status", store.CreateSessionInput{Initialization: store.EnvironmentSetup{Commands: commands[1:]}},
			failure{fail: "setup", result: leakyReceipt("")},
			"Failed to provision environment: initialization did not complete", []string{"configure", "setup"}},
		{"unknown effect", store.CreateSessionInput{Initialization: store.EnvironmentSetup{Commands: commands[1:]}},
			failure{fail: "setup", err: sandbox.ErrCommandUnconfirmed},
			"Failed to provision environment: initialization did not complete", []string{"configure", "setup"}},
		{"output instead of a receipt", store.CreateSessionInput{Initialization: store.EnvironmentSetup{Commands: commands[1:]}},
			failure{fail: "setup", result: sandbox.CommandResult{ExitCode: 3, Stdout: hostedFailureCanary, Stderr: hostedFailureCanary}},
			"Failed to provision environment: initialization did not complete", []string{"configure", "setup"}},
		{"initial file", store.CreateSessionInput{InitialFiles: []store.InitialFile{{Type: "inline", Path: "/workspace/a", Data: []byte(hostedFailureCanary)}}},
			failure{fail: "file", result: sandbox.CommandResult{Stdout: `{"version":1,"outcome":"failed","error":"write_failed","detail":"` + hostedFailureCanary + `"}`}},
			"Failed to provision environment: initial file installation failed", []string{"file"}},
		{"Skill", store.CreateSessionInput{Initialization: store.EnvironmentSetup{Skills: []store.EnvironmentSkill{hostedFailureSkill(t)}, Commands: commands[2:]}},
			failure{fail: "skill", result: leakyReceipt("")},
			"Failed to provision environment: Skill installation failed", []string{"configure", "skill"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := hostedFailureStore(t)
			tenant := uuid.NewString()
			session, environment := hostedFailureSession(t, s, tenant, test.input)
			p := &hostedFailureProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}},
				fail: test.p.fail, skip: test.p.skip, result: test.p.result, err: test.p.err}
			failHostedInitialization(t, s, tenant, environment, p)
			if !reflect.DeepEqual(p.steps, test.steps) || p.kills != 1 {
				t.Fatal("failed initialization continued or was not reclaimed", p.steps, p.kills)
			}

			read, err := s.GetSession(t.Context(), tenant, session.ID)
			if err != nil || read.Environment.Status != "failed" || read.EnvironmentFailure == nil || read.EnvironmentFailure.Reason != test.reason || read.EnvironmentInputActivity != nil || read.LastTurn != nil {
				t.Fatal("Session read", read.EnvironmentFailure, err)
			}
			page, err := s.ListSessions(t.Context(), tenant, "", 10, false, nil)
			if err != nil || len(page.Sessions) != 1 || !reflect.DeepEqual(page.Sessions[0].EnvironmentFailure, read.EnvironmentFailure) {
				t.Fatal("Session list", page, err)
			}
			events, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
			if err != nil || len(events) != 3 {
				t.Fatal("failure events", events, err)
			}
			if event := events[0].Event; event.Type != "agent.session.environment.failed" || event.Environment == nil || event.Environment.ID != environment.ID ||
				event.Environment.Status != "failed" || event.Environment.Type != "openai_hosted" ||
				!reflect.DeepEqual(event.Environment.Error, &v1.StreamError{Type: "environment_error", Code: "environment_connection_failed", Message: "The environment failed to connect."}) {
				t.Fatal("environment failure event", event)
			}
			if event := events[1].Event; event.Type != "error" || !reflect.DeepEqual(event.Error, &v1.StreamError{Type: "environment_error", Code: "sandbox_error", Message: test.reason}) {
				t.Fatal("error event", event)
			}
			last := events[2]
			if last.Event.Type != "agent.session.failed" || last.EnvironmentFailure == nil || last.EnvironmentFailure.Reason != test.reason ||
				!last.EnvironmentFailure.FailedAt.Equal(read.EnvironmentFailure.FailedAt) || last.EnvironmentInputActivity != nil || !last.Settled {
				t.Fatal("failed snapshot", last)
			}
			if _, err := s.ReserveEnvironmentInput(t.Context(), tenant, session.ID, "later", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"later"}`)}}); !errors.Is(err, store.ErrHostedEnvironmentFailed) {
				t.Fatal("failed hosted Environment admitted input", err)
			}
			raw, _ := json.Marshal(events)
			if strings.Contains(string(raw), hostedFailureCanary) || strings.Contains(test.reason, hostedFailureCanary) {
				t.Fatal("initialization output reached public events")
			}
			// Tenant B cannot observe the failure.
			other := uuid.NewString()
			if _, err := s.GetSession(t.Context(), other, session.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("foreign Session read", err)
			}
			if _, err := s.ListSessionEvents(t.Context(), other, session.ID, 0); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("foreign Session events", err)
			}
			if page, err := s.ListSessions(t.Context(), other, "", 10, false, nil); err != nil || len(page.Sessions) != 0 {
				t.Fatal("foreign Session list", page, err)
			}
		})
	}
}

// A pending initial input settles exactly as before; the one failed snapshot
// carries both that settlement and the provisioning failure.
func TestHostedInitializationFailureSettlesPendingInitialInput(t *testing.T) {
	s := hostedFailureStore(t)
	tenant := uuid.NewString()
	session, environment := hostedFailureSession(t, s, tenant, store.CreateSessionInput{
		Initialization: store.EnvironmentSetup{Commands: []store.SetupCommand{{Command: "exit 3"}}},
		InitialInputs:  []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"initial"}`)}},
	})
	p := &hostedFailureProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}}, fail: "setup",
		result: sandbox.CommandResult{ExitCode: 1, Stdout: `{"version":1,"outcome":"failed","exit_code":3}`}}
	failHostedInitialization(t, s, tenant, environment, p)
	read, err := s.GetSession(t.Context(), tenant, session.ID)
	if err != nil || read.PendingInput || read.EnvironmentInputActivity == nil || read.EnvironmentInputActivity.Status != "failed" ||
		read.EnvironmentInputActivity.Failure != "environment_unavailable" || read.EnvironmentFailure == nil {
		t.Fatal("pending input settlement", read.EnvironmentInputActivity, read.EnvironmentFailure, err)
	}
	events, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, event := range events {
		types = append(types, event.Event.Type)
	}
	if !reflect.DeepEqual(types[len(types)-3:], []string{"agent.session.environment.failed", "error", "agent.session.failed"}) || strings.Count(strings.Join(types, " "), "agent.session.failed") != 1 {
		t.Fatal("failure events", types)
	}
	if last := events[len(events)-1]; last.EnvironmentInputActivity == nil || last.EnvironmentInputActivity.Status != "failed" || last.EnvironmentFailure == nil {
		t.Fatal("failed snapshot", last)
	}
}

// H1/H5/H6/H7 over HTTP: retrieve, list and the live stream agree; the GET
// stream ends after agent.session.failed; later input gets the observed 409;
// delete succeeds; tenant B sees nothing; the canary never appears.
func TestHostedInitializationFailurePublicHTTP(t *testing.T) {
	s := hostedFailureStore(t)
	tenant, token, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	session, environment := hostedFailureSession(t, s, tenant, store.CreateSessionInput{
		Initialization: store.EnvironmentSetup{Commands: []store.SetupCommand{{Command: "echo " + hostedFailureCanary + "; exit 3"}}},
		Metadata:       map[string]string{"case": "setup-exit3"},
	})
	key := uuid.NewString()
	// The failed receipt carries canary output in fields Core must never read.
	p := &hostedFailureProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}}, fail: "setup", result: leakyReceipt(`"exit_code":3,`)}
	logs := &lockedBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	w, _ := managedWorker(t, s, key, p)
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(token), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "tenant-b", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(s, auth, "codex", api.WithExecution(w))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	var bodies []string
	call := func(credential, method, path, body string) (int, map[string]any) {
		t.Helper()
		request, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+credential)
		request.Header.Set("OpenAI-Beta", "agents=v1")
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		bodies = append(bodies, string(raw))
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		return response.StatusCode, value
	}

	live := openStream(t, server, token, http.MethodGet, "/v1/agents/sessions/"+session.ID+"/events", "", "")
	defer live.stop()
	if line := live.next(t); line != ": connected" {
		t.Fatal(line)
	}
	if _, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, key); err != nil {
		t.Fatal(err)
	}
	reconcileManagedState(t, w, s, tenant, environment.ID, "released")

	reason := `Failed to provision environment: script "setup_commands[0]" failed with exit code 3`
	read, err := s.GetSession(t.Context(), tenant, session.ID)
	if err != nil || read.EnvironmentFailure == nil {
		t.Fatal(err)
	}
	failedAt := float64(read.EnvironmentFailure.FailedAt.Unix())
	checkSession := func(value any) {
		t.Helper()
		got, _ := value.(map[string]any)
		if got["id"] != session.ID || got["status"] != "failed" || got["error"] != reason || got["last_active_at"] != failedAt ||
			!reflect.DeepEqual(got["required_actions"], []any{}) || got["usage"] != nil {
			t.Fatal("failed Session projection", got)
		}
	}
	var frames []map[string]any
	var names []string
	for len(frames) < 3 {
		line := live.next(t)
		if name, ok := strings.CutPrefix(line, "event: "); ok {
			names = append(names, name)
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		var frame map[string]any
		if !ok || json.Unmarshal([]byte(data), &frame) != nil {
			t.Fatal("invalid frame", line)
		}
		bodies = append(bodies, data)
		frames = append(frames, frame)
	}
	live.ended(t, 5*time.Second)
	if !reflect.DeepEqual(names, []string{"agent.session.environment.failed", "error", "agent.session.failed"}) {
		t.Fatal("stream order", names)
	}
	if !reflect.DeepEqual(frames[0]["environment"], map[string]any{"id": environment.ID, "type": "openai_hosted", "status": "failed",
		"error": map[string]any{"type": "environment_error", "code": "environment_connection_failed", "message": "The environment failed to connect."}}) {
		t.Fatal("environment.failed frame", frames[0])
	}
	if !reflect.DeepEqual(frames[1], map[string]any{"type": "error", "event_id": frames[1]["event_id"], "session_id": session.ID,
		"error": map[string]any{"type": "environment_error", "code": "sandbox_error", "message": reason, "param": nil}}) {
		t.Fatal("error frame", frames[1])
	}
	checkSession(frames[2]["session"])

	if status, body := call(token, http.MethodGet, "/v1/agents/sessions/"+session.ID, ""); status != http.StatusOK {
		t.Fatal(status, body)
	} else {
		checkSession(body)
	}
	if status, body := call(token, http.MethodGet, "/v1/agents/sessions", ""); status != http.StatusOK {
		t.Fatal(status, body)
	} else if data, _ := body["data"].([]any); len(data) != 1 {
		t.Fatal("list", body)
	} else {
		checkSession(data[0])
	}
	input := `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"Reply with OK."}]}]}]}`
	conflict := map[string]any{"error": map[string]any{"type": "conflict_error", "code": "conflict_error", "message": "the hosted environment failed to provision", "param": nil}}
	if status, body := call(token, http.MethodPost, "/v1/agents/sessions/"+session.ID+"/events", input); status != http.StatusConflict || !reflect.DeepEqual(body, conflict) {
		t.Fatal("later input", status, body)
	}
	for _, request := range [][2]string{{http.MethodGet, ""}, {http.MethodGet, "/events"}, {http.MethodPost, "/events"}, {http.MethodDelete, ""}} {
		body := ""
		if request[0] == http.MethodPost {
			body = input
		}
		if status, got := call(foreign, request[0], "/v1/agents/sessions/"+session.ID+request[1], body); status != http.StatusNotFound {
			t.Fatal("tenant B", request, status, got)
		}
	}
	if status, body := call(token, http.MethodDelete, "/v1/agents/sessions/"+session.ID, ""); status != http.StatusOK ||
		!reflect.DeepEqual(body, map[string]any{"id": session.ID, "object": "agent.session.deleted", "deleted": true}) {
		t.Fatal("delete", status, body)
	}
	if status, _ := call(token, http.MethodGet, "/v1/agents/sessions/"+session.ID, ""); status != http.StatusNotFound {
		t.Fatal("deleted Session remained", status)
	}
	for _, body := range bodies {
		if strings.Contains(body, hostedFailureCanary) {
			t.Fatal("initialization output reached a response", body)
		}
	}
	// Logs were captured (the failed step is logged) and hold no output either.
	if logged := logs.String(); !strings.Contains(logged, "managed Runtime file initialization incomplete") || strings.Contains(logged, hostedFailureCanary) {
		t.Fatal("log capture", logged)
	}
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
