package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

// File reads have their own readiness and must not depend on native readiness.
// Withholding ready/started at the actual gateway fixes each lifecycle boundary;
// the timers below only bound a broken test, not the interleaving.
func TestEnvironmentDirectoryPublicIndependentOfNativeReadiness(t *testing.T) {
	for _, phase := range []string{"native_preparing", "started_not_published"} {
		t.Run(phase, func(t *testing.T) {
			h, w, environment := localWorkerForSession(t, true, `{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)
			awaitFixtureCapabilities(t, h, workerEnvironmentCapabilities())
			token := uuid.NewString()
			auth := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "test-org", ProjectID: h.tenant, SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: h.tenant}})
			handler, err := publicHandler(t, h.s, auth, "codex", workerExecution(t, w), acceptUnavailable(t))
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(handler)
			defer server.Close()
			defer server.CloseClientConnections()
			type response struct {
				status          int
				body, requestID string
				err             error
			}
			send := func(method, path, body string) <-chan response {
				done := make(chan response, 1)
				go func() {
					r, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
					if err != nil {
						done <- response{err: err}
						return
					}
					r.Header.Set("Authorization", "Bearer "+token)
					r.Header.Set("OpenAI-Beta", "agents=v1")
					r.Header.Set("Content-Type", "application/json")
					resp, err := server.Client().Do(r)
					if err != nil {
						done <- response{err: err}
						return
					}
					defer resp.Body.Close()
					data, err := io.ReadAll(resp.Body)
					done <- response{resp.StatusCode, string(data), resp.Header.Get("X-Request-ID"), err}
				}()
				return done
			}
			await := func(done <-chan response) response {
				t.Helper()
				select {
				case got := <-done:
					if got.err != nil {
						t.Fatal(got.err)
					}
					return got
				case <-time.After(5 * time.Second):
					t.Fatal("public request did not finish at the held lifecycle boundary")
					return response{}
				}
			}
			readFiles := func() response {
				t.Helper()
				files := send(http.MethodGet, "/v1/agents/environments/"+environment.ID+"/files?path=/workspace&limit=100&order=asc", "")
				frame := h.read(proto.TypeExecutionPrepare)
				var preparation proto.ExecutionPreparePayload
				if frame.DecodePayload(&preparation) != nil || !proto.ValidWorkspaceReadPreparation(preparation.Configuration) || preparation.SessionID != h.session.ID || preparation.Configuration.LocalEnvironment.ID != environment.ID {
					t.Fatal("file read did not prepare its own exact Environment")
				}
				handle := acknowledgePreparation(h, frame.ID)
				h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
				read := h.read(proto.TypeWorkspaceRead)
				var request proto.WorkspaceReadPayload
				if read.DecodePayload(&request) != nil || request.Handle != handle || request.EnvironmentID != environment.ID || request.Path != "" {
					t.Fatal("file read lost its independent owner", request)
				}
				completeDirectoryRead(t, h, frame.ID, read.ID, false, false)
				return await(files)
			}
			admitted := send(http.MethodPost, "/v1/agents/sessions/"+h.session.ID+"/events", `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"work"}]}]}]}`)
			prepare := h.read(proto.TypeExecutionPrepare)
			handle := acknowledgePreparation(h, prepare.ID)
			var got response
			if phase == "native_preparing" {
				session, err := sessionAdapter(h.s).GetSession(t.Context(), h.tenant, h.session.ID)
				if err != nil || session.LastTurn != nil {
					t.Fatal("preparing unexpectedly promoted a Turn", err)
				}
				got = readFiles()
				t.Log("preparation held before ready; LastTurn=nil; no native Start sent")
			}
			h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
			frame := h.read(proto.TypeExecutionStart)
			var start proto.ExecutionStartPayload
			if frame.DecodePayload(&start) != nil || start.RunID == "" {
				t.Fatal("execution did not start")
			}
			if accepted := await(admitted); accepted.status != http.StatusAccepted {
				t.Fatalf("message admission: %+v", accepted)
			}
			if phase == "started_not_published" {
				session, err := sessionAdapter(h.s).GetSession(t.Context(), h.tenant, h.session.ID)
				if err != nil || session.LastTurn == nil || session.LastTurn.ID != start.RunID || session.LastTurn.Status != sessions.TurnInProgress {
					t.Fatal("message admission did not commit the in-progress Turn", err)
				}
				got = readFiles()
				t.Log("events.message=202; Turn=in_progress; started publication held; File read settled through its independent owner")
			}
			// Settle the execution after the independent read has released ownership.
			h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
			h.write(start.RunID, proto.TypeDone, proto.DonePayload{})
			completeEmptyArtifactExport(t, h)
			awaitDaemonRemoteCondition(t, t.Context(), 5*time.Second, "completed Turn after barrier release", func() bool {
				turn, err := sessionAdapter(h.s).GetTurn(t.Context(), h.tenant, h.session.ID, start.RunID)
				if err != nil {
					t.Fatal(err)
				}
				return turn.Status == sessions.TurnCompleted
			})
			assertPreparationReleased(t, h, prepare.ID, handle)
			if got.status != http.StatusOK {
				t.Errorf("File readiness depends on native readiness: status=%d request_id=%s body=%s", got.status, got.requestID, got.body)
			} else {
				var page v1.EnvironmentFileList
				if err := json.Unmarshal([]byte(got.body), &page); err != nil || page.HasMore || len(page.Data) != 1 || page.Data[0].Path != "/workspace/report.txt" || page.Data[0].SizeBytes != 9 || page.Data[0].EnvironmentID != environment.ID {
					t.Errorf("independent File read lost the exact page: %s (decode=%v)", got.body, err)
				}
			}
		})
	}
}
