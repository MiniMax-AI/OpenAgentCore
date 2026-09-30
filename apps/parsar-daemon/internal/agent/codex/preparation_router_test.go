package codex

import "github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"

import (
	"context"
	"errors"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto/prototest"
)

type preparationWireSender chan proto.Envelope

func (s preparationWireSender) Send(ctx context.Context, env proto.Envelope) error {
	select {
	case s <- env:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestPreparationRouterRetainsActualNativeChild(t *testing.T) {
	for _, start := range []bool{false, true} {
		t.Run(map[bool]string{false: "disconnect-before-start", true: "transfer-and-cancel"}[start], func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("OAC_RUNTIME_CAPABILITY_DIRECTORY", filepath.Join(t.TempDir(), "capabilities"))
			t.Setenv("OAC_TEST_EXECUTOR_MODE", "complete")
			environment, session := uuid.NewString(), uuid.NewString()
			if err := os.MkdirAll(req.WorkDir, 0700); err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{
				"OAC_RUNTIME_ENVIRONMENT_ID": environment,
				"OAC_RUNTIME_SESSION_ID":     session,
				"OAC_RUNTIME_WORKSPACE":      req.WorkDir,
				"OAC_RUNTIME_NETWORK_ACCESS": "enabled",
			} {
				t.Setenv(key, value)
			}
			binding, err := localworkspace.Load()
			if err != nil {
				t.Fatal(err)
			}
			req.WorkDir = ""
			req.AgentStateKey = "agents-api-" + session
			req.DisableExecutionEnvironment = false
			req.LocalEnvironment = &proto.LocalEnvironment{ID: environment, WorkspaceDirectory: "/workspace", NetworkAccess: "enabled", CapabilitySources: &agentcapabilities.Input{}}
			registry := agent.NewRegistry()
			registry.RegisterKind(proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilitySupported, FunctionTools: proto.CapabilitySupported})}, harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
				return nil, errors.New("ordinary Factory must not run")
			})
			prepared := make(chan *Prepared, 1)
			registry.RegisterExecutor("codex", func(ctx context.Context, req proto.PromptRequestPayload) (agent.Executor, error) {
				e, err := newExecutor(ctx, req, cfg)
				if err != nil {
					return nil, err
				}
				prepared <- e.prepared
				return e, nil
			})
			sender := make(preparationWireSender, 64)
			r, err := dispatch.New(dispatch.Config{Registry: registry, Sender: sender, LocalWorkspace: binding})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				if err := r.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			send := func(kind, id string, payload any) {
				t.Helper()
				env, err := proto.NewEnvelope(kind, id, payload)
				if err != nil {
					t.Fatal(err)
				}
				if err = r.Handle(t.Context(), env); err != nil {
					t.Fatal(err)
				}
			}
			await := func(state string) proto.PreparationStatusPayload {
				t.Helper()
				timer := time.NewTimer(4 * time.Second)
				defer timer.Stop()
				for {
					select {
					case env := <-sender:
						var status proto.PreparationStatusPayload
						if env.Type == proto.TypePreparationStatus && env.DecodePayload(&status) == nil {
							if status.State == "failed" || status.State == "rejected" {
								t.Fatal(status.State, status.ErrorCode)
							}
							if status.State == state {
								return status
							}
						}
					case <-timer.C:
						t.Fatal("preparation status missing", state)
						return proto.PreparationStatusPayload{}
					}
				}
			}
			send(proto.TypeExecutionPrepare, "prepare-request", proto.ExecutionPreparePayload{SessionID: session, Configuration: req})
			ready := await("ready")
			p := <-prepared
			assertPreparationOnly(t, root)
			pid := p.session.rpc.cmd.Process.Pid
			if r.ActiveRuns() != 0 {
				t.Fatal("preparation became a Run")
			}
			if start {
				input := proto.ExecutionStartPayload{ExecutorID: ready.ExecutorID, Handle: ready.Handle, RunID: "actual-run", Input: proto.TextInput("hold")}
				send(proto.TypeExecutionStart, "prepare-request", input)
				await("started")
				frames := waitPreparationMethod(t, root, "turn/start")
				turns := 0
				for _, frame := range frames {
					if frame.PID != pid {
						t.Fatal("native child changed")
					}
					if frame.Method == "turn/start" {
						turns++
					}
				}
				if turns != 1 {
					t.Fatal("unexpected native Turn count", turns)
				}
				send(proto.TypeExecutionStart, "prepare-request", input)
				send(proto.TypeExecutionRelease, "prepare-request", proto.ExecutionReleasePayload{Handle: ready.Handle})
				if !p.session.rpc.Alive() || r.ActiveRuns() != 1 {
					t.Fatal("release cancelled transferred native session")
				}
				send(proto.TypePromptCancel, "actual-run", proto.PromptCancelPayload{})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			if err := r.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			waitPreparedRelease(t, p, root)
			if !start {
				assertPreparationOnly(t, root)
			}
		})
	}
}
