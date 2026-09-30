package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func initializationState(t *testing.T, s *store.Store, tenant, environment string) string {
	t.Helper()
	value, err := s.GetEnvironment(t.Context(), tenant, environment)
	if err != nil {
		t.Fatal(err)
	}
	return value.Initialization
}

func awaitInitialization(t *testing.T, s *store.Store, tenant, environment, state string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if initializationState(t, s, tenant, environment) == state {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("initialization did not reach %s", state)
}

func TestUserManagedPreparationUsesAuthenticatedRuntimeWithoutAllocation(t *testing.T) {
	for _, outcome := range []string{"completed", "failed", "unknown", "unavailable", "revoked"} {
		t.Run(outcome, func(t *testing.T) {
			_, pool := store.NewManagedTestStore(t)
			cipher, err := credentialcrypto.New(bytes.Repeat([]byte{7}, 32))
			if err != nil {
				t.Fatal(err)
			}
			s := store.NewWithCredentialCipher(pool, cipher)
			principal := store.FixtureExecutorPrincipal(t, s, uuid.NewString())
			session, err := s.CreateSession(t.Context(), principal.TenantID, store.CreateSessionInput{
				Creator: principal.Subject(), Engine: "codex", IdempotencyKey: uuid.NewString(),
				Configuration:  json.RawMessage(`{"environment":{"type":"self_hosted","workspace_directory":"/home/user/work"}}`),
				InitialFiles:   []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/input", Data: []byte("frozen")}},
				Initialization: environmentconfig.Setup{Skills: []environmentconfig.Skill{hostedFailureSkill(t)}, Env: map[string]string{"EXPLICIT": "value"}, Commands: []environmentconfig.SetupCommand{{Command: "touch setup"}}, CapabilityDirectories: []string{"/home/user/capabilities"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			environment, err := s.GetSessionEnvironment(t.Context(), principal.TenantID, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			key, err := s.IssueExecutorCredential(t.Context(), principal, uuid.NewString(), environment.ID)
			if err != nil {
				t.Fatal(err)
			}
			enrolled, err := s.EnrollRuntime(t.Context(), environment.ID, runtimedevice.HashCredential(key.Token))
			if err != nil {
				t.Fatal(err)
			}
			registry := runtimegateway.NewRegistry()
			handler := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Authenticator: runtimegateway.NewAuthenticator(s), Registry: registry})
			server := httptest.NewServer(http.HandlerFunc(handler.WS))
			defer server.Close()
			worker, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s, Registry: registry})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			defer func() {
				cancel()
				if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
			}()
			var mu sync.Mutex
			var actions []string
			peer := &initializationPeer{unavailable: outcome == "unavailable"}
			peer.setRuntimeGateway(t, "ws"+strings.TrimPrefix(server.URL, "http"), registry)
			peer.apply = func(request proto.RuntimePreparePayload, data []byte) proto.RuntimePrepareResultPayload {
				if request.EnvironmentID != environment.ID || request.SessionID != session.ID {
					t.Error("wrong authorization binding")
				}
				action := request.Action
				if request.Initialization != nil {
					action = request.Initialization.Action
				}
				if action == "file" && !bytes.Equal(data, []byte("frozen")) {
					t.Error("file snapshot changed")
				}
				mu.Lock()
				actions = append(actions, action)
				mu.Unlock()
				if outcome == "revoked" {
					if err := s.RevokeDevice(t.Context(), principal.TenantID, enrolled.DeviceID); err != nil {
						t.Error(err)
					}
					return completedInitialization(request, data)
				}
				if outcome != "completed" {
					return proto.RuntimePrepareResultPayload{Outcome: outcome, ErrorCode: "runtime_preparation_unconfirmed"}
				}
				return completedInitialization(request, data)
			}
			time.Sleep(350 * time.Millisecond)
			if initializationState(t, s, principal.TenantID, environment.ID) != "pending" {
				t.Fatal("unconnected preparation was consumed")
			}
			bootstrap := sandbox.Bootstrap{DeviceID: enrolled.DeviceID, Credential: key.Token}
			if err := peer.connect(bootstrap); err != nil {
				t.Fatal(err)
			}
			want := "complete"
			if outcome != "completed" {
				want = "failed"
			}
			awaitInitialization(t, s, principal.TenantID, environment.ID, want)
			if _, err := s.GetRuntimeAllocation(t.Context(), principal.TenantID, environment.ID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("self-hosted preparation fabricated allocation", err)
			}
			if outcome == "completed" {
				mu.Lock()
				got := strings.Join(actions, ",")
				mu.Unlock()
				if got != "file,configure,skill,setup,finalize" {
					t.Fatal("common preparation order", got)
				}
				if err := peer.connect(bootstrap); err != nil {
					t.Fatal(err)
				}
				time.Sleep(350 * time.Millisecond)
				if peer.writes.Load() != 5 {
					t.Fatal("reconnect reinstalled snapshot", peer.writes.Load())
				}
			} else {
				expected := int32(1)
				if outcome == "unavailable" {
					expected = 0
				}
				if peer.writes.Load() != expected {
					t.Fatal("failed, unavailable or revoked effect replayed", peer.writes.Load())
				}
				value, err := s.GetSession(t.Context(), principal.TenantID, session.ID)
				if err != nil || value.EnvironmentFailure == nil {
					t.Fatal("failure lost shared Session state", err)
				}
			}
		})
	}
}

// Revocation can commit after the worker observes a connected peer but before
// it claims preparation. It must remain a per-Environment admission result.
func TestEnvironmentInitializationRevocationBeforeClaim(t *testing.T) {
	s, _ := store.NewManagedTestStore(t)
	principal := store.FixtureExecutorPrincipal(t, s, uuid.NewString())
	create := func() store.EnvironmentInitialization {
		t.Helper()
		session, err := s.CreateSession(t.Context(), principal.TenantID, store.CreateSessionInput{
			Creator: principal.Subject(), Engine: "codex", IdempotencyKey: uuid.NewString(),
			Configuration: json.RawMessage(`{"environment":{"type":"self_hosted","workspace_directory":"/home/user/work"}}`),
			InitialFiles:  []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/input", Data: []byte("frozen")}},
		})
		if err != nil {
			t.Fatal(err)
		}
		environment, err := s.GetSessionEnvironment(t.Context(), principal.TenantID, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		key, err := s.IssueExecutorCredential(t.Context(), principal, uuid.NewString(), environment.ID)
		if err != nil {
			t.Fatal(err)
		}
		enrolled, err := s.EnrollRuntime(t.Context(), environment.ID, runtimedevice.HashCredential(key.Token))
		if err != nil {
			t.Fatal(err)
		}
		return store.EnvironmentInitialization{EnvironmentID: environment.ID, SessionID: session.ID, TenantID: principal.TenantID, DeviceID: enrolled.DeviceID, State: "pending", Engine: "codex"}
	}
	revoked, other := create(), create()
	owned, err := store.NewExecution(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer owned.CloseExecution(context.Background())
	if err := s.RevokeDevice(t.Context(), principal.TenantID, revoked.DeviceID); err != nil {
		t.Fatal(err)
	}
	if err := owned.ClaimEnvironmentInitialization(t.Context(), revoked); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revocation escaped normal admission handling: %v", err)
	}
	stale := other
	stale.DeviceID = uuid.NewString()
	if err := owned.ClaimEnvironmentInitialization(t.Context(), stale); !errors.Is(err, store.ErrTurnConflict) {
		t.Fatalf("stale binding escaped normal admission handling: %v", err)
	}
	if initializationState(t, s, other.TenantID, other.EnvironmentID) != "pending" {
		t.Fatal("stale claim changed preparation state")
	}
	if err := owned.ClaimEnvironmentInitialization(t.Context(), other); err != nil {
		t.Fatal("unrelated Environment could not prepare", err)
	}
	if err := owned.CompleteEnvironmentInitialization(t.Context(), other); err != nil {
		t.Fatal(err)
	}
}
