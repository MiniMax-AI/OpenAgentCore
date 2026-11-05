package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func TestStandaloneEnvironmentOfficialClientPostgres(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, _ := configuredStore(t)
	tenant, foreignTenant, token, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "prewarm-owner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "prewarm-foreign", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: foreignTenant},
	})
	handler, err := publicHandler(t, s, auth, "codex", fixtureDeploymentProvider())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	settings, err := json.Marshal(map[string]string{"base": server.URL, "token": token, "foreign": foreign})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python, "../../tests/official_environments.py")
	command.Stdin = bytes.NewReader(settings)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("standalone Environment official client: %v %s", err, output)
	}
	var receipt struct {
		EnvironmentID string `json:"environment_id"`
		SessionID     string `json:"session_id"`
	}
	if err := json.Unmarshal(output, &receipt); err != nil {
		t.Fatalf("invalid acceptance receipt: %v %s", err, output)
	}
	t.Cleanup(func() {
		if err := sessionService(t, s).DeleteSession(context.Background(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: receipt.SessionID}); err != nil {
			t.Error(err)
		}
	})
	// The Environment, not the Session, owns the row, and survives the first Session.
	var owned bool
	var attached int
	if err := s.pool.QueryRow(t.Context(), "SELECT e.session_owned, (SELECT count(*) FROM sessions WHERE environment_id=e.id) FROM environments e WHERE e.id=$1 AND e.tenant_id=$2", receipt.EnvironmentID, tenant).Scan(&owned, &attached); err != nil || owned || attached != 2 {
		t.Fatalf("standalone Environment ownership: owned=%v sessions=%d %v", owned, attached, err)
	}
}

// Concurrent attaches of one standalone Environment have exactly one winner.
func TestConcurrentAttachHasOneWinnerPostgres(t *testing.T) {
	s, _ := configuredStore(t)
	tenant, token := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "attach-owner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant}})
	handler, err := publicHandler(t, s, auth, "codex", fixtureDeploymentProvider())
	if err != nil {
		t.Fatal(err)
	}
	post := func(path, body string) (int, string) {
		request := httptest.NewRequest("POST", "/v1"+path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("OpenAI-Beta", "agents=v1")
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	code, body := post("/agents/environments", `{"environment":{"type":"openai_hosted"}}`)
	var environment struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal([]byte(body), &environment) != nil {
		t.Fatal("create Environment", code, body)
	}
	codes := make([]int, 6)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() {
			codes[i], _ = post("/agents/sessions", `{"agent":{"model":"fixture"},"environment":{"type":"openai_hosted","environment_id":"`+environment.ID+`"}}`)
		})
	}
	wg.Wait()
	created := 0
	for _, code := range codes {
		switch code {
		case 201:
			created++
		case 409:
		default:
			t.Fatal("unexpected attach status", codes)
		}
	}
	if created != 1 {
		t.Fatal("attach winners", codes)
	}
}
