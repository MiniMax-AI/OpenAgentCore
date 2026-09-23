package store_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// storeAdmission admits creation input through the Store without a Worker, so
// reservations stay pending until the test settles them.
type storeAdmission struct{ *store.Store }

type sseLines struct {
	lines chan string
	stop  context.CancelFunc
}

// openStream reads non-empty SSE lines until the server ends the response.
func openStream(t *testing.T, server *httptest.Server, token, method, path, body, key string) sseLines {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("OpenAI-Beta", "agents=v1")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode/100 != 2 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("stream was not opened", response.StatusCode)
	}
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			if line := scanner.Text(); line != "" {
				lines <- line
			}
		}
	}()
	return sseLines{lines: lines, stop: cancel}
}

func (s sseLines) next(t *testing.T) string {
	t.Helper()
	select {
	case line, ok := <-s.lines:
		if !ok {
			t.Fatal("stream ended early")
		}
		return line
	case <-time.After(5 * time.Second):
		t.Fatal("stream line timed out")
	}
	return ""
}

func (s sseLines) ended(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case line, ok := <-s.lines:
		if ok {
			t.Fatal("stream sent more after settlement", line)
		}
	case <-time.After(within):
		t.Fatal("stream did not end once the Session settled")
	}
}

// drain reads lines until the stream is quiet, failing if it ends.
func (s sseLines) drain(t *testing.T) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				t.Fatal("stream ended early")
			}
			seen[line] = true
		case <-time.After(500 * time.Millisecond):
			return seen
		}
	}
}

func (s sseLines) open(t *testing.T) {
	t.Helper()
	select {
	case line, ok := <-s.lines:
		t.Fatal("stream changed while it should wait", line, ok)
	case <-time.After(1500 * time.Millisecond):
	}
}

// A self_hosted creation without input ends after its created snapshot, and a
// same-key stream retry ends at once even while later input is pending. A fresh
// creation stream whose initial reservation is cancelled without a Session event
// ends through the committed projection, while GET stays open.
func TestCreationStreamPublicLifetimes(t *testing.T) {
	s, pool := store.NewTestStore(t)
	tenant, token := uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(token), TenantID: tenant}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(s, auth, "codex", api.WithEnvironmentRemoteURL("https://offline-executor.example"), api.WithExecution(storeAdmission{s}))
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	lease, err := s.AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close(context.Background()) }()
	writer := lease.Store()
	connect := func(environment string) {
		t.Helper()
		generation := uuid.NewString()
		if err := writer.ReplaceEnvironmentConnection(t.Context(), tenant, environment, generation); err != nil {
			t.Fatal(err)
		}
		if err := writer.ObserveEnvironmentConnection(t.Context(), tenant, environment, generation, 1, true); err != nil {
			t.Fatal(err)
		}
	}
	type createdEvent struct {
		Session struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			Environment struct {
				ID string `json:"id"`
			} `json:"environment"`
		} `json:"session"`
	}
	readCreated := func(stream sseLines, status string) createdEvent {
		t.Helper()
		if line := stream.next(t); line != ": connected" {
			t.Fatal(line)
		}
		if line := stream.next(t); line != "event: agent.session.created" {
			t.Fatal(line)
		}
		var event createdEvent
		if data, ok := strings.CutPrefix(stream.next(t), "data: "); !ok || json.Unmarshal([]byte(data), &event) != nil || event.Session.Status != status {
			t.Fatal("invalid created snapshot", data)
		}
		return event
	}
	const idle = `{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"},"stream":true}`
	const initial = `{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"},"input":"initial","stream":true}`

	created := openStream(t, server, token, http.MethodPost, "/v1/agents/sessions", idle, "no-input")
	defer created.stop()
	first := readCreated(created, "idle")
	created.ended(t, 5*time.Second)

	connect(first.Session.Environment.ID)
	if _, err := s.ReserveEnvironmentInput(t.Context(), tenant, first.Session.ID, "later", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"later"}`)}}); err != nil {
		t.Fatal(err)
	}
	if current, err := s.GetSession(t.Context(), tenant, first.Session.ID); err != nil || !current.PendingInput {
		t.Fatal("later input is not pending", err)
	}
	retry := openStream(t, server, token, http.MethodPost, "/v1/agents/sessions", idle, "no-input")
	defer retry.stop()
	if line := retry.next(t); line != ": connected" {
		t.Fatal(line)
	}
	retry.ended(t, 5*time.Second)

	fresh := openStream(t, server, token, http.MethodPost, "/v1/agents/sessions", initial, "initial-input")
	defer fresh.stop()
	second := readCreated(fresh, "requires_action")
	session := second.Session.ID
	if line := fresh.next(t); line != "event: agent.session.requires_action" {
		t.Fatal(line)
	}
	fresh.next(t)
	live := openStream(t, server, token, http.MethodGet, "/v1/agents/sessions/"+session+"/events", "", "")
	defer live.stop()
	if line := live.next(t); line != ": connected" {
		t.Fatal(line)
	}
	// The connection clears the action to idle; the initial input is still pending.
	connect(second.Session.Environment.ID)
	if seen := fresh.drain(t); !seen["event: agent.session.idle"] || !seen["event: agent.session.environment.connected"] || seen["event: agent.session.failed"] {
		t.Fatal("unexpected connection events", seen)
	}
	fresh.open(t)
	var reservation string
	if err := pool.QueryRow(t.Context(), "SELECT id FROM environment_input_reservations WHERE session_id=$1 AND is_initial", session).Scan(&reservation); err != nil {
		t.Fatal(err)
	}
	cursor, err := s.SessionEventCursor(t.Context(), tenant, session)
	if err != nil {
		t.Fatal(err)
	}
	if settled, err := s.CancelEnvironmentInput(t.Context(), tenant, session, reservation); err != nil || settled.State != store.EnvironmentInputCancelled {
		t.Fatal(settled.State, err)
	}
	if after, err := s.SessionEventCursor(t.Context(), tenant, session); err != nil || after != cursor {
		t.Fatal("cancellation recorded a Session event; this case needs a silent settlement", after, cursor, err)
	}
	fresh.ended(t, 5*time.Second)
	live.drain(t)
	live.open(t)
	live.stop()
	deadline := time.Now().Add(3 * time.Second)
	for active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active.Load() != 0 {
		t.Fatal("stream handler leaked")
	}
}
