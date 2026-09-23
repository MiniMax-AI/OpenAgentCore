package store_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// Whitespace-only user text is admitted at Session creation and events.create
// and its Items keep the exact text (SES-01..04). Empty text, content and input
// still reject with today's fields and write nothing.
func TestWhitespaceInputStoredVerbatimPostgres(t *testing.T) {
	// An isolated database keeps the no-write digest independent of other tests.
	s, pool := store.NewManagedTestStore(t)
	token := uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "whitespace-owner", TokenSHA256: device.HashCredential(token), TenantID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(s, auth, "codex", api.WithExecution(s))
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
