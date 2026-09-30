package store_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type initializingProvider struct {
	lifecycleProvider
	initializationPeer
}

func (p *initializingProvider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	info, err := p.lifecycleProvider.Create(ctx, b)
	if err == nil {
		err = p.connect(b)
	}
	return info, err
}
func (p *initializingProvider) RunCommand(ctx context.Context, r sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	return p.initializationPeer.RunCommand(ctx, r, c)
}

func TestEnvironmentInitializationCompletionUnknownAndRestart(t *testing.T) {
	for _, mode := range []string{"complete", "restart", "uncertain", "setup-complete", "setup-restart", "setup-uncertain"} {
		t.Run(mode, func(t *testing.T) {
			setupOnly := strings.HasPrefix(mode, "setup-")
			mode = strings.TrimPrefix(mode, "setup-")
			expectedSteps := 2
			_, pool := store.NewManagedTestStore(t)
			cipher, err := credentialcrypto.New(bytes.Repeat([]byte{9}, 32))
			if err != nil {
				t.Fatal(err)
			}
			s, db := store.NewWithCredentialCipher(pool, cipher), fixtureDB{pool: pool, cipher: cipher}
			tenant := uuid.NewString()
			input := sessions.CreateSession{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), InitialFiles: []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/a", Data: []byte("first")}, {Type: "inline", Path: "/workspace/b", Data: []byte("second")}}}
			if setupOnly {
				input.InitialFiles = nil
				input.Initialization = environmentconfig.Setup{Env: map[string]string{"VALUE": "private"}, Packages: v1.EnvironmentPackages{NPM: []string{"is-number@7.0.0"}}, Commands: []environmentconfig.SetupCommand{{Command: "touch first"}, {Command: "test -f first"}}}
				expectedSteps = 4
			}
			session, err := s.CreateSession(t.Context(), tenant, input)
			if err != nil {
				t.Fatal(err)
			}
			env, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "restart" {
				if _, err := pool.Exec(t.Context(), "UPDATE environments SET initialization='running' WHERE id=$1", env.ID); err != nil {
					t.Fatal(err)
				}
			}
			p := &initializingProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}}, initializationPeer: initializationPeer{deferred: true}}
			p.apply = func(_ proto.RuntimePreparePayload, _ []byte) proto.RuntimePrepareResultPayload {
				if _, err := s.GetSessionDevice(t.Context(), tenant, session.ID); !errors.Is(err, sessions.ErrNotFound) {
					t.Error("premature file access", err)
				}
				if _, err := s.GetSessionExecutionBinding(t.Context(), tenant, session.ID); !errors.Is(err, sessions.ErrNotFound) {
					t.Error("premature execution", err)
				}
				if mode == "uncertain" {
					return proto.RuntimePrepareResultPayload{Outcome: "unknown", ErrorCode: "runtime_preparation_unconfirmed"}
				}
				return completedInitialization(proto.RuntimePreparePayload{}, nil)
			}
			key := uuid.NewString()
			w, stop := managedWorkerMode(t, s, db, key, p, false, true)
			if mode == "restart" {
				awaitInitialization(t, s, tenant, env.ID, "failed")
				if p.writes.Load() != 0 {
					t.Fatal("recovered unknown operation replayed")
				}
				return
			}
			owner, err := w.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID); err != nil || !ok {
				t.Fatal("preparation blocked authentication", err)
			}
			time.Sleep(350 * time.Millisecond)
			if initializationState(t, s, tenant, env.ID) != "pending" || p.writes.Load() != 0 {
				t.Fatal("missing socket consumed initialization")
			}
			p.deferred = false
			if err := p.connect(sandbox.Bootstrap{DeviceID: owner.DeviceID, Credential: p.credential}); err != nil {
				t.Fatal(err)
			}
			want := "complete"
			if mode == "uncertain" {
				want = "failed"
			}
			awaitInitialization(t, s, tenant, env.ID, want)
			if mode == "complete" {
				if int(p.writes.Load()) != expectedSteps {
					t.Fatal("missing operations", p.writes.Load())
				}
				if _, err := s.GetSessionExecutionBinding(t.Context(), tenant, session.ID); err != nil {
					t.Fatal("completed preparation blocked", err)
				}
				stop()
				_, _ = managedWorkerMode(t, s, db, key, p, false, true)
				time.Sleep(350 * time.Millisecond)
				if int(p.writes.Load()) != expectedSteps {
					t.Fatal("completed preparation replayed")
				}
			} else if p.writes.Load() != 1 {
				t.Fatal("unknown operation replayed", p.writes.Load())
			}
			p.mu.Lock()
			kills := p.kills
			p.mu.Unlock()
			if kills != 0 || p.commandCalls.Load() != 0 {
				t.Fatal("preparation changed compute lifecycle", kills, p.commandCalls.Load())
			}
		})
	}
}

func TestManagedRuntimePreparationAllOperationsUsePeer(t *testing.T) {
	s, db := hostedFailureStore(t)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for path, body := range map[string]string{"proof/.codex-plugin/plugin.json": `{"name":"plugin","description":"A plugin.","skills":"./skills"}`, "proof/skills/example/SKILL.md": "---\nname: plugin-proof\ndescription: A plugin Skill.\n---\nProof."} {
		file, err := writer.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	tenant := uuid.NewString()
	fileBody := bytes.Repeat([]byte("bounded bytes"), 12000)
	session, environment := hostedFailureSession(t, s, tenant, sessions.CreateSession{
		InitialFiles:   []environmentconfig.InitialFile{{Type: "inline", Path: "/workspace/first", Data: fileBody}},
		Initialization: environmentconfig.Setup{Skills: []environmentconfig.Skill{hostedFailureSkill(t)}, Plugins: []environmentconfig.Plugin{{Metadata: agentplugin.Metadata{Type: "inline", Name: "plugin", Description: "A plugin."}, Archive: archive.Bytes()}}, Packages: v1.EnvironmentPackages{NPM: []string{"is-number@7.0.0"}, Python: []string{"packaging==24.2"}}, Commands: []environmentconfig.SetupCommand{{Command: "read installed bundles and create directory"}}, CapabilityDirectories: []string{"/workspace/generated"}},
	})
	provider := &initializingProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}}}
	var actions []string
	var actionsMu sync.Mutex
	provider.apply = func(request proto.RuntimePreparePayload, data []byte) proto.RuntimePrepareResultPayload {
		if request.SessionID != session.ID || request.EnvironmentID != environment.ID {
			t.Error("Runtime identity changed")
		}
		action := request.Action
		if request.Initialization != nil {
			action = request.Initialization.Action
		}
		if action == "file" && !bytes.Equal(data, fileBody) {
			t.Error("initial bytes changed")
		}
		actionsMu.Lock()
		actions = append(actions, action)
		actionsMu.Unlock()
		return completedInitialization(request, data)
	}
	key := uuid.NewString()
	worker, _ := managedWorkerMode(t, s, db, key, provider, false, true)
	if _, err := worker.ProvisionEnvironment(t.Context(), tenant, environment.ID, key); err != nil {
		t.Fatal(err)
	}
	awaitInitialization(t, s, tenant, environment.ID, "complete")
	actionsMu.Lock()
	defer actionsMu.Unlock()
	expected := []string{"file", "configure", "skill", "plugin", "npm", "python", "setup", "finalize"}
	if !reflect.DeepEqual(actions, expected) || provider.commandCalls.Load() != 0 {
		t.Fatal("typed ordering or provider isolation", actions, provider.commandCalls.Load())
	}
	allocation, err := s.GetRuntimeAllocation(t.Context(), tenant, environment.ID)
	if err != nil || initializationState(t, s, allocation.TenantID, allocation.EnvironmentID) != "complete" {
		t.Fatal("initialization incomplete", allocation, err)
	}
}
