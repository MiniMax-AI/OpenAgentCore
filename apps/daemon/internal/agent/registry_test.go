package agent_test

import "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

func stubFactory(marker string) agent.Factory {
	return func(_ context.Context, _ proto.PromptRequestPayload, _ chan<- proto.Envelope) (agent.Session, error) {
		return stubSession{marker: marker}, nil
	}
}

type stubSession struct{ marker string }

func (stubSession) Cancel(context.Context) error { return nil }
func (stubSession) SubmitPermission(context.Context, string, proto.PermissionDecisionPayload) error {
	return nil
}
func (stubSession) SubmitPromptForUserChoice(context.Context, string, proto.PromptForUserChoiceDecisionPayload) error {
	return nil
}

func TestRegistryResolveReturnsRegisteredFactory(t *testing.T) {
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "claude_code", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, stubFactory("cc"))

	f, err := reg.Resolve("claude_code")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	sess, err := f(context.Background(), proto.PromptRequestPayload{AgentKind: "claude_code"}, nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	stub, ok := sess.(stubSession)
	if !ok || stub.marker != "cc" {
		t.Errorf("resolved factory returned %#v, want stubSession{marker:\"cc\"}", sess)
	}
}

func TestRegistryResolveUnknownKindReturnsTypedError(t *testing.T) {
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "claude_code", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, stubFactory("cc"))

	_, err := reg.Resolve("opencode")
	if !errors.Is(err, agent.ErrUnsupportedKind) {
		t.Errorf("Resolve unknown = %v, want ErrUnsupportedKind chain", err)
	}
}

func TestRegistryRegisterOverwrites(t *testing.T) {
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "k", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, stubFactory("v1"))
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "k", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, stubFactory("v2"))

	f, err := reg.Resolve("k")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	sess, _ := f(context.Background(), proto.PromptRequestPayload{}, nil)
	if got := sess.(stubSession).marker; got != "v2" {
		t.Errorf("overwrite: marker = %q, want v2", got)
	}
}

func TestRegistryKindsReportsRegistered(t *testing.T) {
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "claude_code", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, stubFactory("cc"))
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "opencode", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, stubFactory("oc"))

	got := reg.Kinds()
	slices.Sort(got)
	want := []string{"claude_code", "opencode"}
	if !slices.Equal(got, want) {
		t.Errorf("Kinds = %v, want %v", got, want)
	}
}

func TestRegistryRegisterPanicsOnEmptyKind(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(\"\", ...) did not panic")
		}
	}()
	agent.NewRegistry().RegisterKind(proto.SupportedAgentKind{Kind: "", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, stubFactory("x"))
}

func TestRegistryRegisterPanicsOnNilFactory(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(kind, nil) did not panic")
		}
	}()
	agent.NewRegistry().RegisterKind(proto.SupportedAgentKind{Kind: "k", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, nil)
}

func TestRegistrySupportedAgentKindsReportsDescriptors(t *testing.T) {
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{
		Kind:      "opencode",
		Available: false,
		Version:   "missing",
		Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
			Streaming: proto.CapabilitySupported,
		}),
	}, harnessconfig.Configuration{}, stubFactory("oc"))
	reg.RegisterKind(proto.SupportedAgentKind{
		Kind:      "claude_code",
		Available: true,
		Version:   "1.2.3",
		Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
			Streaming:   proto.CapabilitySupported,
			Permissions: proto.CapabilitySupported,
			Usage:       proto.CapabilitySupported,
			Resume:      proto.CapabilitySupported,
		}),
	}, harnessconfig.Configuration{}, stubFactory("cc"))

	got := reg.SupportedAgentKinds()
	if len(got) != 2 {
		t.Fatalf("SupportedAgentKinds len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Kind != "claude_code" || got[1].Kind != "opencode" {
		t.Fatalf("SupportedAgentKinds sort = %#v, want claude_code then opencode", got)
	}
	if !got[0].Available || got[0].Version != "1.2.3" || !got[0].Capabilities.Permissions.IsSupported() || !got[0].Capabilities.Resume.IsSupported() {
		t.Fatalf("claude_code descriptor not preserved: %#v", got[0])
	}
	if got[1].Available || got[1].Version != "missing" || !got[1].Capabilities.Streaming.IsSupported() {
		t.Fatalf("opencode descriptor not preserved: %#v", got[1])
	}
}

func TestRegistryExecutorRequiresExplicitRegistration(t *testing.T) {
	registry := agent.NewRegistry()
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "native", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, stubFactory("native"))
	if _, err := registry.ResolveExecutor("native"); err == nil {
		t.Fatal("legacy factory implied reusable execution")
	}
	expected := errors.New("executor factory")
	registry.RegisterExecutor("native", func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) { return nil, expected })
	factory, err := registry.ResolveExecutor("native")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := factory(t.Context(), proto.PromptRequestPayload{}); !errors.Is(err, expected) {
		t.Fatal(err)
	}
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "native", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{}, stubFactory("replacement"))
	if _, err := registry.ResolveExecutor("native"); err == nil {
		t.Fatal("replacing a kind retained its old executor capability")
	}
}

func TestRegistryRejectsEveryOmittedCapabilityBeforeReplacement(t *testing.T) {
	valid := prototest.Capabilities(proto.AgentKindCapabilities{})
	for i := 0; i < reflect.TypeOf(valid).NumField(); i++ {
		t.Run(reflect.TypeOf(valid).Field(i).Name, func(t *testing.T) {
			registry := agent.NewRegistry()
			registry.RegisterKind(proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: valid}, harnessconfig.Configuration{}, stubFactory("original"))
			missing := valid
			reflect.ValueOf(&missing).Elem().Field(i).Set(reflect.ValueOf(proto.CapabilityUnspecified))
			func() {
				defer func() {
					if recover() == nil {
						t.Error("incomplete declaration registered")
					}
				}()
				registry.RegisterKind(proto.SupportedAgentKind{Kind: "fixture", Available: false, Capabilities: missing}, harnessconfig.Configuration{}, stubFactory("replacement"))
			}()
			factory, err := registry.Resolve("fixture")
			if err != nil {
				t.Fatal(err)
			}
			session, err := factory(t.Context(), proto.PromptRequestPayload{}, nil)
			if err != nil || session.(stubSession).marker != "original" {
				t.Fatal("failed declaration changed registry")
			}
		})
	}
}
