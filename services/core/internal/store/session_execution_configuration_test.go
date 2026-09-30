package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

func executionProjectionInput(source string) CreateSessionInput {
	model, harness := "frozen-model", "codex"
	return CreateSessionInput{
		Creator: FixtureCreator(), Engine: harness, IdempotencyKey: uuid.NewString(),
		Configuration: []byte(`{"agent":{"model":"frozen-model"},"environment":{"type":"openai_hosted"}}`),
		ExecutionConfiguration: &v1.SessionExecutionConfiguration{
			Model:         v1.ExecutionSelection{Value: &model, Source: source},
			Harness:       v1.ExecutionSelection{Value: &harness, Source: source},
			ModelProvider: v1.ExecutionProviderSelection{Source: "unknown", Status: "unavailable"},
		},
	}
}

func TestSessionExecutionConfigurationFrozenAcrossCreationPathsAndRetry(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{41}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	for _, stream := range []bool{false, true} {
		for _, source := range []string{"session", "agent", "deployment"} {
			t.Run(source+map[bool]string{false: "/ordinary", true: "/stream"}[stream], func(t *testing.T) {
				tenant := uuid.NewString()
				input := executionProjectionInput(source)
				input.ModelProvider = &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://private-deployment.example/v1", APIKey: "private-projection-key-canary"}
				input.ExecutionConfiguration.ModelProvider = v1.ExecutionProviderSelection{Source: source, Status: "available", Configuration: input.ModelProvider.SafeView()}
				input.ExecutionConfiguration.Object = "untrusted-object"
				input.ExecutionConfiguration.SchemaVersion = 99
				input.ExecutionConfiguration.SessionID = "untrusted-session"
				var session Session
				var err error
				if stream {
					created, e := s.CreateSessionStream(t.Context(), tenant, input)
					session, err = created.Session, e
				} else {
					session, err = s.CreateSession(t.Context(), tenant, input)
				}
				if err != nil {
					t.Fatal(err)
				}
				// A reader without the encryption key can use the safe snapshot after restart.
				reader := New(pool)
				frozen, err := reader.GetSessionExecutionConfiguration(t.Context(), tenant, session.ID)
				if err != nil {
					t.Fatal(err)
				}
				if frozen.Object != "agent.session.execution_configuration" || frozen.SchemaVersion != 1 || frozen.SessionID != session.ID || frozen.Model.Source != source || frozen.Harness.Source != source {
					t.Fatal("incorrect frozen projection identity or provenance")
				}
				// Deployment defaults are readable with the same Core key, so every
				// source records the safe view of the frozen bundle.
				if frozen.ModelProvider.Status != "available" || !reflect.DeepEqual(frozen.ModelProvider.Configuration, input.ModelProvider.SafeView()) {
					t.Fatal("safe provider projection changed")
				}
				var stored []byte
				if err := pool.QueryRow(t.Context(), "SELECT configuration FROM session_execution_configuration WHERE session_id=$1", session.ID).Scan(&stored); err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{"private-projection-key-canary", "native_options"} {
					if bytes.Contains(stored, []byte(secret)) {
						t.Fatal("secret entered safe projection")
					}
				}
				// Source-only changes and invalid replacement metadata never alter the
				// existing Session's retry identity or projection.
				input.ExecutionConfiguration.Model.Source = "different-on-retry"
				input.ExecutionConfiguration.ModelProvider = v1.ExecutionProviderSelection{Source: "unknown", Status: "unavailable"}
				replay, err := s.CreateSession(t.Context(), tenant, input)
				if err != nil || replay.ID != session.ID {
					t.Fatal("projection metadata changed retry identity", err)
				}
				if _, err := s.UpdateSessionMetadata(t.Context(), tenant, session.ID, map[string]string{"edited": "yes"}); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(t.Context(), "UPDATE session_model_execution SET encrypted_config='\\x00'::bytea WHERE session_id=$1", session.ID); err != nil {
					t.Fatal(err)
				}
				got, err := reader.GetSessionExecutionConfiguration(t.Context(), tenant, session.ID)
				if err != nil || !reflect.DeepEqual(got, frozen) {
					t.Fatal("snapshot changed or reader decrypted a secret", err)
				}
				if source == "deployment" {
					// Sessions frozen before deployment defaults moved into Core stay redacted.
					if _, err := pool.Exec(t.Context(), `UPDATE session_execution_configuration SET configuration = jsonb_set(configuration, '{model_provider}', '{"source":"deployment","status":"redacted","configuration":null}') WHERE session_id=$1`, session.ID); err != nil {
						t.Fatal(err)
					}
					if historical, err := reader.GetSessionExecutionConfiguration(t.Context(), tenant, session.ID); err != nil || historical.ModelProvider.Status != "redacted" || historical.ModelProvider.Configuration != nil {
						t.Fatal("historical deployment selection changed", err)
					}
				}
				for _, lookup := range []struct{ tenant, id string }{{uuid.NewString(), session.ID}, {tenant, uuid.NewString()}, {tenant, "malformed"}} {
					if _, err := reader.GetSessionExecutionConfiguration(t.Context(), lookup.tenant, lookup.id); !errors.Is(err, ErrNotFound) {
						t.Fatal("foreign/missing projection read differed", err)
					}
				}
				if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := reader.GetSessionExecutionConfiguration(t.Context(), tenant, session.ID); !errors.Is(err, ErrNotFound) {
					t.Fatal("deleted Session projection remained public", err)
				}
			})
		}
	}
}

func TestSessionExecutionConfigurationHistoricalProvenance(t *testing.T) {
	s, pool := testStore(t)
	tenant := uuid.NewString()
	for _, configuration := range []string{`{}`, `{"agent":{"model":null}}`, `{"agent":{"model":"historical-model"},"model_provider_configured":true}`} {
		input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: []byte(configuration)}
		session, err := s.CreateSession(t.Context(), tenant, input)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.GetSessionExecutionConfiguration(t.Context(), tenant, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Model.Source != "unknown" || got.Harness.Source != "unknown" || got.Harness.Value == nil || *got.Harness.Value != "codex" || got.ModelProvider.Source != "unknown" || got.ModelProvider.Status != "unavailable" || got.ModelProvider.Configuration != nil {
			t.Fatal("historical provenance guessed")
		}
		if bytes.Contains([]byte(configuration), []byte("historical-model")) {
			if got.Model.Value == nil || *got.Model.Value != "historical-model" {
				t.Fatal("historical model lost")
			}
		} else if got.Model.Value != nil {
			t.Fatal("missing historical model invented")
		}
		input.ExecutionConfiguration = executionProjectionInput("session").ExecutionConfiguration
		if _, err := s.CreateSession(t.Context(), tenant, input); err != nil {
			t.Fatal("historical retry changed", err)
		}
		var count int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM session_execution_configuration WHERE session_id=$1", session.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("historical snapshot was backfilled", err)
		}
	}
}

func TestSessionExecutionConfigurationRollbackAndValidation(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	tenant := uuid.NewString()
	for _, kind := range []string{"model", "harness", "source", "provider", "provider_mismatch", "post_projection_failure"} {
		input := executionProjectionInput("session")
		switch kind {
		case "model":
			value := "different-model"
			input.ExecutionConfiguration.Model.Value = &value
		case "harness":
			value := "different-harness"
			input.ExecutionConfiguration.Harness.Value = &value
		case "source":
			input.ExecutionConfiguration.Model.Source = "invalid"
		case "provider":
			input.ExecutionConfiguration.ModelProvider = v1.ExecutionProviderSelection{Source: "session", Status: "available", Configuration: &v1.ModelProviderView{Protocol: "responses", BaseURL: "https://example.com"}}
		case "provider_mismatch":
			input.ModelProvider = &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://actual.example/v1", APIKey: "private-key-canary"}
			input.ExecutionConfiguration.ModelProvider = v1.ExecutionProviderSelection{Source: "session", Status: "available", Configuration: input.ModelProvider.SafeView()}
			input.ExecutionConfiguration.ModelProvider.Configuration.BaseURL = "https://different.example/v1"
		case "post_projection_failure":
			input.InitialFiles = []environmentconfig.InitialFile{{Type: "inline", Path: "invalid-path"}}
		}
		if _, err := s.CreateSession(t.Context(), tenant, input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: invalid projection/creation accepted: %v", kind, err)
		}
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE tenant_id=$1", tenant).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected projection left Session", err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM session_execution_configuration p LEFT JOIN sessions s ON s.id=p.session_id WHERE s.id IS NULL").Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected creation left orphan projection", err)
	}
}

func TestSessionExecutionConfigurationConcurrentRetryKeepsWinner(t *testing.T) {
	s, _ := testStore(t)
	tenant := uuid.NewString()
	input := executionProjectionInput("session")
	var wg sync.WaitGroup
	projections := make(chan v1.SessionExecutionConfiguration, 8)
	for i := 0; i < 8; i++ {
		request := input
		projection := *input.ExecutionConfiguration
		if i%2 == 1 {
			projection.Model.Source = "agent"
			projection.Harness.Source = "deployment"
		}
		request.ExecutionConfiguration = &projection
		wg.Add(1)
		go func() {
			defer wg.Done()
			created, err := s.CreateSessionStream(t.Context(), tenant, request)
			if err != nil {
				t.Error(err)
				return
			}
			got, err := s.GetSessionExecutionConfiguration(t.Context(), tenant, created.Session.ID)
			if err != nil {
				t.Error(err)
				return
			}
			projections <- got
		}()
	}
	wg.Wait()
	close(projections)
	var winner *v1.SessionExecutionConfiguration
	for got := range projections {
		if winner == nil {
			winner = &got
		} else if !reflect.DeepEqual(*winner, got) {
			t.Fatal("concurrent retry rewrote provenance")
		}
	}
	if winner == nil {
		t.Fatal("no committed projection")
	}
}

func TestSessionExecutionConfigurationSurvivesSuspendResume(t *testing.T) {
	s, pool := testStore(t)
	w := executionWriter(t, s)
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, executionProjectionInput("agent"))
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := s.GetSessionExecutionConfiguration(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, environment.ID, uuid.NewString(), runtimedevice.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	owner, err = w.ObserveRuntimeRunning(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = w.SetRuntimeCompute(t.Context(), owner, "running", json.RawMessage(`{"instance":"original"}`), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	runtimeSuspensionCompleted(t, pool, owner)
	until := time.Now().Add(time.Hour)
	for _, phase := range []string{"quiescing", "suspending", "suspended", "restoring", "waking", "running"} {
		retained := &until
		if phase == "running" {
			retained = nil
		}
		owner = runtimeSuspensionStep(t, w, owner, phase, retained)
		before, err := w.RuntimeActivity(t.Context(), owner)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.GetSessionExecutionConfiguration(t.Context(), tenant, session.ID)
		if err != nil || !reflect.DeepEqual(got, frozen) {
			t.Fatal("runtime transition changed execution projection", phase, err)
		}
		after, err := w.RuntimeActivity(t.Context(), owner)
		if err != nil || !before.LastActivity.Equal(after.LastActivity) || before.WakeRequested != after.WakeRequested || before.Busy != after.Busy || before.HasCompletedTurn != after.HasCompletedTurn {
			t.Fatal("configuration read changed runtime activity", err)
		}
	}
}
