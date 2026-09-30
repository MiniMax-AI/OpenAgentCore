package store_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestLiveStreamClosesAfterKeyRevocationOrProjectArchive(t *testing.T) {
	for _, archive := range []bool{false, true} {
		name := "key-revocation"
		if archive {
			name = "project-archive"
		}
		t.Run(name, func(t *testing.T) {
			s, _ := store.NewTestStore(t)
			projectID := uuid.NewString()
			ctx := adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "12345678", ActorLabel: "test", RequestID: uuid.NewString(), TraceID: uuid.NewString(), ProjectID: projectID})
			project, err := s.CreateProject(ctx, projectID, "Stream authority")
			if err != nil {
				t.Fatal(err)
			}
			reader, err := s.CreateProjectAPIKey(ctx, project.ID, uuid.NewString(), "reader")
			if err != nil {
				t.Fatal(err)
			}
			peer, err := s.CreateProjectAPIKey(ctx, project.ID, uuid.NewString(), "peer")
			if err != nil {
				t.Fatal(err)
			}
			session, err := s.CreateSession(t.Context(), project.TenantID, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"id":"agent_fixture","model":"fixture","tools":[]},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)})
			if err != nil {
				t.Fatal(err)
			}
			auth, err := api.NewDatabaseAuthenticator(s)
			if err != nil {
				t.Fatal(err)
			}
			h, err := api.NewHandler(s, auth, "codex", api.WithEnvironmentRemoteURL("wss://core.example/api/v1/agent-daemon/ws"))
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(h)
			defer server.Close()
			request := func(key, suffix string) *http.Response {
				t.Helper()
				r, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+"/v1/agents/sessions/"+session.ID+suffix, nil)
				r.Header.Set("Authorization", "Bearer "+key)
				r.Header.Set("OpenAI-Beta", "agents=v1")
				response, err := server.Client().Do(r)
				if err != nil {
					t.Fatal(err)
				}
				return response
			}
			response := request(reader.Key, "/events")
			defer response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("stream status", response.StatusCode)
			}
			input := bufio.NewReader(response.Body)
			if line, err := input.ReadString('\n'); err != nil || line != ": connected\n" {
				t.Fatal("stream not open", err)
			}
			// Keep an idle stream open across a successful authority recheck.
			time.Sleep(1100 * time.Millisecond)
			if archive {
				_, err = s.ArchiveProject(ctx, project.ID)
			} else {
				err = s.RevokeProjectAPIKey(ctx, project.ID, reader.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			fresh := request(reader.Key, "")
			fresh.Body.Close()
			if fresh.StatusCode != 401 {
				t.Fatal("fresh request retained authority", fresh.StatusCode)
			}
			done := make(chan error, 1)
			go func() { _, err := io.Copy(io.Discard, input); done <- err }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("revoked idle stream remained open")
			}
			if !archive {
				valid := request(peer.Key, "")
				valid.Body.Close()
				if valid.StatusCode != 200 {
					t.Fatal("revocation affected peer", valid.StatusCode)
				}
				// New events remain available to valid callers after the reader has closed.
				if _, err := s.ReserveEnvironmentInput(t.Context(), project.TenantID, session.ID, "after-revocation", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"new event"}`)}}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
