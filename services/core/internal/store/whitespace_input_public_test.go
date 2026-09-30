package store_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

// Whitespace-only user text is admitted at Session creation and events.create
// and its Items keep the exact text (SES-01..04). Empty text, content and input
// still reject with today's fields and write nothing.
func TestWhitespaceInputStoredVerbatimPostgres(t *testing.T) {
	// An isolated database keeps the no-write digest independent of other tests.
	s, pool := store.NewManagedTestStore(t)
	token := uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "whitespace-owner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: uuid.NewString()}})
	h, err := publicHandler(t, s, auth, "codex", storeExecution(t, s))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	client := pathIDClient{t: t, server: server}
	create := func(input string) string {
		return client.created(token, "/v1/agents/sessions", `{"agent":{"model":"whitespace-model"},"environment":{"type":"none"},"input":`+input+`}`)
	}

	// W1 and W2: string and message-item input.
	for _, tc := range []struct {
		input string
		texts [][]string
	}{
		{`"   "`, [][]string{{"   "}}},
		{`"\n\t"`, [][]string{{"\n\t"}}},
		{`[{"role":"user","content":[{"type":"input_text","text":"\n\t"}]}]`, [][]string{{"\n\t"}}},
		{`[{"type":"message","role":"user","content":[{"type":"input_text","text":"   "}]},{"role":"user","content":[{"type":"input_text","text":" \t\n "}]}]`, [][]string{{"   "}, {" \t\n "}}},
	} {
		session := create(tc.input)
		if got := userTexts(t, client, token, session); !reflect.DeepEqual(got, tc.texts) {
			t.Errorf("create %s: Items %q", tc.input, got)
		}
	}

	// W3 and W5: events.create on an idle Session starts a Turn with exact Items.
	session := create(`"Start."`)
	if status, body := client.do(token, http.MethodPost, "/v1/agents/sessions/"+session+"/events", "application/json", []byte(`{"events":[{"type":"agent.session.input.cancel"}]}`)); status != http.StatusAccepted {
		t.Fatalf("cancel: %d %s", status, body)
	}
	want := [][]string{{"Start."}}
	for _, tc := range []struct {
		input string
		texts [][]string
	}{
		{`[{"role":"user","content":[{"type":"input_text","text":"   "}]},{"role":"user","content":[{"type":"input_text","text":"\n\t"}]}]`, [][]string{{"   "}, {"\n\t"}}},
		{`[{"role":"user","content":[{"type":"input_text","text":""},{"type":"input_text","text":"Reply only OK."}]}]`, [][]string{{"", "Reply only OK."}}},
	} {
		if status, body := client.do(token, http.MethodPost, "/v1/agents/sessions/"+session+"/events", "application/json", []byte(`{"events":[{"type":"agent.session.input.message","input":`+tc.input+`}]}`)); status != http.StatusAccepted || body != "" {
			t.Fatalf("events %s: %d %s", tc.input, status, body)
		}
		want = append(want, tc.texts...)
		if got := userTexts(t, client, token, session); !reflect.DeepEqual(got, want) {
			t.Errorf("events %s: Items %q", tc.input, got)
		}
	}

	// W4: unchanged rejection without writes.
	before := databaseDigest(t, pool)
	const rejection = `{"error":{"message":"Invalid resource identifier or request limits.","type":"invalid_request_error","code":"invalid_request","param":null}}` + "\n"
	for _, input := range []string{`""`, `[]`, `[{"role":"user","content":[]}]`, `[{"role":"user","content":[{"type":"input_text","text":""}]}]`} {
		if status, body := client.do(token, http.MethodPost, "/v1/agents/sessions", "application/json", []byte(`{"agent":{"model":"whitespace-model"},"environment":{"type":"none"},"input":`+input+`}`)); status != http.StatusBadRequest || body != rejection {
			t.Errorf("create %s: %d %s", input, status, body)
		}
	}
	for _, input := range []string{`[]`, `[{"role":"user","content":[]}]`, `[{"role":"user","content":[{"type":"input_text","text":""}]}]`, `[{"role":"user","content":[{"type":"input_text","text":" "}]},{"role":"user","content":[{"type":"input_text","text":""}]}]`} {
		if status, body := client.do(token, http.MethodPost, "/v1/agents/sessions/"+session+"/events", "application/json", []byte(`{"events":[{"type":"agent.session.input.message","input":`+input+`}]}`)); status != http.StatusBadRequest || body != rejection {
			t.Errorf("events %s: %d %s", input, status, body)
		}
	}
	if after := databaseDigest(t, pool); !mapsEqual(before, after) {
		t.Error("rejected empty input changed persisted state")
	}
}

// W6: harness profiles declare whether whitespace-only text is qualified. Codex
// admits it; Claude SDK and MiniMax Code reject it at Session creation and
// events.create, before any write, reservation or promotion.
func TestWhitespaceOnlyTextHarnessAdmissionPostgres(t *testing.T) {
	s, pool := store.NewManagedTestStore(t)
	token := uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "whitespace-harness", TokenSHA256: runtimedevice.HashCredential(token), TenantID: uuid.NewString()}})
	// Real Worker admission with dispatch paused keeps admitted Turns queued.
	worker, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s, Registry: runtimegateway.NewRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopped, cancel := context.WithCancel(context.Background())
		cancel()
		if err := worker.Run(stopped); err != context.Canceled {
			t.Error(err)
		}
	})
	serve := func(engine string) pathIDClient {
		handler, err := publicHandler(t, s, auth, engine, workerExecution(worker), executorURL("https://offline-executor.example"))
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return pathIDClient{t: t, server: server}
	}
	events := func(client pathIDClient, session, body string) (int, string) {
		return client.do(token, http.MethodPost, "/v1/agents/sessions/"+session+"/events", "application/json", []byte(body))
	}
	const cancel = `{"events":[{"type":"agent.session.input.cancel"}]}`
	const whitespace = `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"   "}]},{"role":"user","content":[{"type":"input_text","text":"\n\t"}]}]}]}`

	codex := serve("codex")
	admitted := codex.created(token, "/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"none"},"input":"   "}`)
	if status, body := events(codex, admitted, cancel); status != http.StatusAccepted {
		t.Fatalf("codex cancel: %d %s", status, body)
	}
	if status, body := events(codex, admitted, whitespace); status != http.StatusAccepted {
		t.Fatalf("codex events: %d %s", status, body)
	}
	if got := userTexts(t, codex, token, admitted); !reflect.DeepEqual(got, [][]string{{"   "}, {"   "}, {"\n\t"}}) {
		t.Errorf("codex Items %q", got)
	}

	const rejection = `{"error":{"message":"This Session's harness does not accept a message whose text is only whitespace. Include non-whitespace text or an image, or use a harness that supports whitespace-only text.","type":"invalid_request_error","code":"unsupported_or_invalid_configuration","param":null}}` + "\n"
	for _, engine := range []string{"claude_sdk", "mcode"} {
		client := serve(engine)
		session := client.created(token, "/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"none"},"input":"Start."}`)
		if status, body := events(client, session, cancel); status != http.StatusAccepted {
			t.Fatalf("%s cancel: %d %s", engine, status, body)
		}
		before := databaseDigest(t, pool)
		for _, body := range []string{
			`{"agent":{"model":"m"},"environment":{"type":"none"},"input":"   "}`,
			`{"agent":{"model":"m"},"environment":{"type":"none"},"input":[{"role":"user","content":[{"type":"input_text","text":"\n\t"}]}]}`,
			`{"agent":{"model":"m"},"environment":{"type":"none"},"input":[{"role":"user","content":[{"type":"input_text","text":"x"}]},{"role":"user","content":[{"type":"input_text","text":""},{"type":"input_text","text":" "}]}]}`,
			`{"agent":{"model":"m"},"environment":{"type":"none"},"stream":true,"input":"   "}`,
			// The self-hosted initial reservation is never created.
			`{"agent":{"model":"m"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"},"input":"   ",` + fixtureSessionProvider(engine) + `}`,
		} {
			if status, response := client.do(token, http.MethodPost, "/v1/agents/sessions", "application/json", []byte(body)); status != http.StatusBadRequest || response != rejection {
				t.Errorf("%s create %s: %d %s", engine, body, status, response)
			}
		}
		for _, body := range []string{whitespace, `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"x"}]},{"role":"user","content":[{"type":"input_text","text":"\t"}]}]}]}`} {
			if status, response := events(client, session, body); status != http.StatusBadRequest || response != rejection {
				t.Errorf("%s events %s: %d %s", engine, body, status, response)
			}
		}
		if after := databaseDigest(t, pool); !mapsEqual(before, after) {
			t.Errorf("%s: rejected whitespace-only text changed persisted state", engine)
		}
		// Whitespace beside non-whitespace text in one message remains admitted verbatim.
		if status, body := events(client, session, `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"   "},{"type":"input_text","text":"Reply only OK."}]}]}]}`); status != http.StatusAccepted {
			t.Fatalf("%s mixed events: %d %s", engine, status, body)
		}
		if got := userTexts(t, client, token, session); !reflect.DeepEqual(got, [][]string{{"Start."}, {"   ", "Reply only OK."}}) {
			t.Errorf("%s Items %q", engine, got)
		}
	}
}

// userTexts returns the text parts of each user message Item in ascending order.
func userTexts(t *testing.T, client pathIDClient, token, session string) [][]string {
	t.Helper()
	status, body := client.do(token, http.MethodGet, "/v1/agents/sessions/"+session+"/items?order=asc&limit=100", "", nil)
	var page struct {
		Data []struct {
			Type, Role string
			Content    []struct {
				Type string
				Text *string
			}
		}
	}
	if status != http.StatusOK || json.Unmarshal([]byte(body), &page) != nil {
		t.Fatalf("items: %d %s", status, body)
	}
	var texts [][]string
	for _, item := range page.Data {
		if item.Type != "message" || item.Role != "user" {
			continue
		}
		var parts []string
		for _, part := range item.Content {
			if part.Type != "input_text" || part.Text == nil {
				t.Fatalf("user content changed: %s", body)
			}
			parts = append(parts, *part.Text)
		}
		texts = append(texts, parts)
	}
	return texts
}
