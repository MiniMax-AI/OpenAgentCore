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

// publicPoster returns a request function for the public API under one Project key.
func publicPoster(t *testing.T, s *Store, tenant, subject string) func(method, path, body string, headers ...string) (int, string) {
	t.Helper()
	token := uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: subject, TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant}})
	handler, err := publicHandler(t, s, auth, "codex", fixtureDeploymentProvider())
	if err != nil {
		t.Fatal(err)
	}
	return func(method, path, body string, headers ...string) (int, string) {
		request := httptest.NewRequest(method, "/v1"+path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("OpenAI-Beta", "agents=v1")
		request.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(headers); i += 2 {
			request.Header.Set(headers[i], headers[i+1])
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
}

func createStandalone(t *testing.T, request func(method, path, body string, headers ...string) (int, string)) string {
	t.Helper()
	code, body := request("POST", "/agents/environments", `{"environment":{"type":"openai_hosted"}}`)
	var environment struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal([]byte(body), &environment) != nil {
		t.Fatal("create Environment", code, body)
	}
	return environment.ID
}

// Concurrent attaches of one standalone Environment have exactly one winner,
// and the others get the attach conflict.
func TestConcurrentAttachHasOneWinnerPostgres(t *testing.T) {
	s, _ := configuredStore(t)
	request := publicPoster(t, s, uuid.NewString(), "attach-owner")
	environment := createStandalone(t, request)
	codes, bodies := make([]int, 6), make([]string, 6)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() {
			codes[i], bodies[i] = request("POST", "/agents/sessions", `{"agent":{"model":"fixture"},"environment":{"type":"openai_hosted","environment_id":"`+environment+`"}}`)
		})
	}
	wg.Wait()
	created := 0
	for i, code := range codes {
		switch {
		case code == 201:
			created++
		case code == 409 && strings.Contains(bodies[i], "Environment is attached to another session."):
		default:
			t.Fatal("unexpected attach outcome", code, bodies[i])
		}
	}
	if created != 1 {
		t.Fatal("attach winners", codes)
	}
}

// An attach waits for the Environment lock without holding the deployment
// lock, so the lock order stays Session, Environment, deployment.
func TestAttachWaitsForTheEnvironmentBeforeTheDeploymentPostgres(t *testing.T) {
	s, _ := configuredStore(t)
	request := publicPoster(t, s, uuid.NewString(), "attach-order")
	environment := createStandalone(t, request)
	held, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(context.Background())
	if _, err := held.Exec(t.Context(), "SELECT 1 FROM environments WHERE id=$1 FOR UPDATE", environment); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		code, _ := request("POST", "/agents/sessions", `{"agent":{"model":"fixture"},"environment":{"type":"openai_hosted","environment_id":"`+environment+`"}}`)
		done <- code
	}()
	for deadline, waiting := time.Now().Add(10*time.Second), 0; waiting == 0; time.Sleep(10 * time.Millisecond) {
		if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 0 && time.Now().After(deadline) {
			t.Fatal("the attach never waited for the Environment lock")
		}
	}
	other, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Rollback(context.Background())
	if _, err := other.Exec(t.Context(), "SELECT 1 FROM runtime_deployment WHERE singleton = true FOR UPDATE NOWAIT"); err != nil {
		t.Fatal("the waiting attach holds the deployment lock", err)
	}
	other.Rollback(t.Context())
	held.Rollback(t.Context())
	if code := <-done; code != 201 {
		t.Fatal("attach status", code)
	}
}

// Another principal in the same Project reads an empty list.
func TestStandaloneEnvironmentListIsScopedToItsCreatorPostgres(t *testing.T) {
	s, _ := configuredStore(t)
	tenant := uuid.NewString()
	owner, other := publicPoster(t, s, tenant, "list-owner"), publicPoster(t, s, tenant, "list-other")
	environment := createStandalone(t, owner)
	list := func(request func(method, path, body string, headers ...string) (int, string)) []string {
		code, body := request("GET", "/agents/environments", "")
		var page struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if code != 200 || json.Unmarshal([]byte(body), &page) != nil {
			t.Fatal("list Environments", code, body)
		}
		ids := []string{}
		for _, item := range page.Data {
			ids = append(ids, item.ID)
		}
		return ids
	}
	if ids := list(owner); len(ids) != 1 || ids[0] != environment {
		t.Fatal("the creator's list", ids)
	}
	if ids := list(other); len(ids) != 0 {
		t.Fatal("another principal's list", ids)
	}
	if code, _ := other("GET", "/agents/environments/"+environment, "", ""); code != 200 {
		t.Fatal("retrieve stays Project-scoped", code)
	}
}

// Concurrent creates under one key return one Environment.
func TestConcurrentCreatesWithOneKeyMakeOneEnvironmentPostgres(t *testing.T) {
	s, _ := configuredStore(t)
	tenant := uuid.NewString()
	request := publicPoster(t, s, tenant, "create-owner")
	ids := make([]string, 6)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			code, body := request("POST", "/agents/environments", `{"environment":{"type":"openai_hosted"}}`, "Idempotency-Key", "one-key")
			var environment struct {
				ID string `json:"id"`
			}
			if code != 201 || json.Unmarshal([]byte(body), &environment) != nil {
				t.Error("create", code, body)
			}
			ids[i] = environment.ID
		})
	}
	wg.Wait()
	var stored int
	if err := s.pool.QueryRow(t.Context(), "SELECT count(*) FROM environments WHERE tenant_id=$1", tenant).Scan(&stored); err != nil || stored != 1 {
		t.Fatal("stored Environments", stored, err)
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatal("creates returned different Environments", ids)
		}
	}
	// The pinned Idempotency-Key is 1 to 256 bytes.
	for _, key := range []string{"", strings.Repeat("k", 257)} {
		if code, body := request("POST", "/agents/environments", `{"environment":{"type":"openai_hosted"}}`, "Idempotency-Key", key); code != 400 {
			t.Error("invalid key", len(key), code, body)
		}
	}
}
