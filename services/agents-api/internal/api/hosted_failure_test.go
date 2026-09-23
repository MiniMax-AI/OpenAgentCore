package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

const hostedFailureReason = `Failed to provision environment: script "setup_commands[0]" failed with exit code 3`

func hostedFailureSession() store.Session {
	session := environmentSession()
	session.Configuration = json.RawMessage(`{"agent":{"id":"agent_test","model":"model","tools":[]},"environment":{"type":"openai_hosted"}}`)
	session.Environment.Configuration = json.RawMessage(`{"type":"openai_hosted"}`)
	session.Environment.Status = "failed"
	return session
}

// A recorded hosted provisioning failure makes the Session failed with the safe
// reason and failure time, whether or not pending input was settled with it.
func TestHostedProvisioningFailureSessionProjection(t *testing.T) {
	failedAt := time.Unix(1790187038, 0)
	for _, activity := range []*store.EnvironmentInputActivity{nil, {Status: "failed", Failure: "environment_unavailable", LastActiveAt: failedAt.Add(-time.Second)}} {
		session := hostedFailureSession()
		session.EnvironmentInputActivity = activity
		session.EnvironmentFailure = &store.EnvironmentFailure{Reason: hostedFailureReason, FailedAt: failedAt}
		response, err := sessionResponse(session, "")
		if err != nil || response.Status != "failed" || response.Error == nil || *response.Error != hostedFailureReason ||
			response.LastActiveAt != failedAt.Unix() || response.RequiredActions == nil || len(response.RequiredActions) != 0 {
			t.Fatal("failed hosted Session projection", response, err)
		}
	}
	// Without a recorded failure (expiry, or failures before this release), the
	// existing projection is unchanged.
	session := hostedFailureSession()
	if response, err := sessionResponse(session, ""); err != nil || response.Status != "idle" || response.Error != nil {
		t.Fatal("unrecorded failure changed the projection", response, err)
	}
	session = environmentSession()
	session.EnvironmentFailure = &store.EnvironmentFailure{Reason: hostedFailureReason, FailedAt: failedAt}
	if _, err := sessionResponse(session, environmentOrigin); err == nil {
		t.Fatal("self-hosted Session accepted a hosted provisioning failure")
	}
}

// The error event carries the pinned SessionError with a null param; the
// Environment state error keeps the observed three fields.
func TestHostedProvisioningFailureEventShapes(t *testing.T) {
	session := hostedFailureSession()
	fields := func(change store.SessionChange) map[string]any {
		t.Helper()
		event, err := streamResponse(session, change, "")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	got := fields(store.SessionChange{Event: v1.SessionEvent{Type: "error", EventID: "event", SessionID: "session",
		Error: &v1.StreamError{Type: "environment_error", Code: "sandbox_error", Message: hostedFailureReason}}})
	want := map[string]any{"type": "error", "event_id": "event", "session_id": "session",
		"error": map[string]any{"type": "environment_error", "code": "sandbox_error", "message": hostedFailureReason, "param": nil}}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("error event", got)
	}
	got = fields(store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.environment.failed", EventID: "event", SessionID: "session",
		Environment: &v1.SessionEnvironmentState{ID: "environment", Type: "openai_hosted", Status: "failed",
			Error: &v1.StreamError{Type: "environment_error", Code: "environment_connection_failed", Message: "The environment failed to connect."}}}})
	if environment, _ := got["environment"].(map[string]any); !reflect.DeepEqual(environment["error"], map[string]any{
		"type": "environment_error", "code": "environment_connection_failed", "message": "The environment failed to connect."}) {
		t.Fatal("environment.failed event", got)
	}
	failedAt := time.Unix(1790187038, 0)
	got = fields(store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.failed", EventID: "event"},
		EnvironmentFailure: &store.EnvironmentFailure{Reason: hostedFailureReason, FailedAt: failedAt}})
	if snapshot, _ := got["session"].(map[string]any); snapshot["status"] != "failed" || snapshot["error"] != hostedFailureReason ||
		snapshot["last_active_at"] != float64(failedAt.Unix()) || !reflect.DeepEqual(snapshot["required_actions"], []any{}) {
		t.Fatal("failed snapshot", got)
	}
}

// H5: a GET stream ends after the agent.session.failed of a hosted provisioning
// failure; a Turn failure leaves it open because the Session can continue.
func TestGetStreamEndsAfterHostedProvisioningFailure(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			f := &streamFixture{session: hostedFailureSession()}
			f.session.TenantID = uuid.NewString()
			f.session.Environment.TenantID = f.session.TenantID
			auth, err := NewAuthenticator([]APIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential("key"), TenantID: f.session.TenantID}})
			if err != nil {
				t.Fatal(err)
			}
			h, err := NewHandler(f, auth, "codex")
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(h)
			defer server.Close()
			failed := store.SessionChange{Sequence: 13, Event: v1.SessionEvent{Type: "agent.session.failed", EventID: "failed"}}
			if terminal {
				failed.EnvironmentFailure = &store.EnvironmentFailure{Reason: hostedFailureReason, FailedAt: time.Unix(1790187038, 0)}
			} else {
				failed.Turn = &store.Turn{ID: "turn", Status: store.TurnFailed}
			}
			f.changes = []store.SessionChange{
				{Sequence: 11, Event: v1.SessionEvent{Type: "agent.session.environment.failed", EventID: "environment", SessionID: "session",
					Environment: &v1.SessionEnvironmentState{ID: "environment", Type: "openai_hosted", Status: "failed", Error: &v1.StreamError{Type: "environment_error", Code: "environment_connection_failed", Message: "The environment failed to connect."}}}},
				{Sequence: 12, Event: v1.SessionEvent{Type: "error", EventID: "error", SessionID: "session", Error: &v1.StreamError{Type: "environment_error", Code: "sandbox_error", Message: hostedFailureReason}}},
				failed,
			}
			request, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/agents/sessions/session/events", nil)
			request.Header.Set("Authorization", "Bearer key")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			// The stream that ends on the failure carries the request headers (HP-23/24).
			if !requestIDPattern.MatchString(response.Header.Get("X-Request-Id")) || response.Header.Get("Openai-Processing-Ms") == "" {
				t.Fatal("stream headers", response.Header)
			}
			lines := make(chan string, 32)
			go func() {
				defer close(lines)
				scanner := bufio.NewScanner(response.Body)
				scanner.Buffer(nil, 1<<20)
				for scanner.Scan() {
					if strings.HasPrefix(scanner.Text(), "event: ") {
						lines <- strings.TrimPrefix(scanner.Text(), "event: ")
					}
				}
			}()
			var names []string
			for len(names) < 3 {
				select {
				case name := <-lines:
					names = append(names, name)
				case <-time.After(5 * time.Second):
					t.Fatal("events not delivered", names)
				}
			}
			if !reflect.DeepEqual(names, []string{"agent.session.environment.failed", "error", "agent.session.failed"}) {
				t.Fatal("event order", names)
			}
			select {
			case _, open := <-lines:
				if open || !terminal {
					t.Fatal("stream lifetime after failure", open, terminal)
				}
			case <-time.After(1500 * time.Millisecond):
				if terminal {
					t.Fatal("GET stream stayed open after the terminal failure")
				}
			}
		})
	}
}

// H6: new input on a failed hosted Environment gets the observed 409; expiry
// keeps the local environment_unavailable response.
func TestHostedProvisioningFailureInputConflict(t *testing.T) {
	for err, want := range map[error]string{
		fmt.Errorf("reserve: %w", store.ErrHostedEnvironmentFailed): `{"error":{"message":"the hosted environment failed to provision","type":"conflict_error","code":"conflict_error","param":null}}`,
		store.ErrEnvironmentUnavailable:                             `{"error":{"message":"The environment is no longer available for new input.","type":"conflict_error","code":"environment_unavailable","param":null}}`,
	} {
		response := httptest.NewRecorder()
		writeStoreError(response, httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/session/events", nil), err)
		body, _ := io.ReadAll(response.Body)
		if response.Code != http.StatusConflict || strings.TrimSpace(string(body)) != want {
			t.Fatal(response.Code, string(body))
		}
	}
	if !errors.Is(store.ErrHostedEnvironmentFailed, store.ErrEnvironmentUnavailable) {
		t.Fatal("internal callers no longer see an unavailable Environment")
	}
}

// Core's own interruption frame keeps the three-field error that released
// clients validate exactly; official error events carry param null.
func TestStreamInterruptionFrameOmitsParam(t *testing.T) {
	var frame []byte
	writeStreamFailure(func(data []byte) error { frame = data; return nil }, "session")
	name, data, ok := strings.Cut(strings.TrimSuffix(string(frame), "\n\n"), "\n")
	var event map[string]any
	if !ok || name != "event: error" || json.Unmarshal([]byte(strings.TrimPrefix(data, "data: ")), &event) != nil {
		t.Fatalf("frame %q", frame)
	}
	want := map[string]any{"type": "error", "event_id": event["event_id"], "session_id": "session", "error": map[string]any{
		"code": "stream_interrupted", "type": "server_error", "message": "The live stream was interrupted. Reconnect and retrieve the Session and its saved Items to recover."}}
	if id, _ := event["event_id"].(string); id == "" || !reflect.DeepEqual(event, want) {
		t.Fatalf("interruption frame %s", data)
	}
}
