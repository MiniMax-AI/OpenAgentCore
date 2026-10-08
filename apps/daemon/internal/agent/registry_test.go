package agent_test

import "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

func TestRegistryRegisterOverwritesDescriptor(t *testing.T) {
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "k", Available: true, Version: "v1", Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{})
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "k", Available: true, Version: "v2", Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{})

	got := reg.SupportedAgentKinds()
	if len(got) != 1 || got[0].Version != "v2" {
		t.Errorf("overwrite: descriptors = %#v, want one v2 descriptor", got)
	}
}

func TestRegistryRegisterPanicsOnEmptyKind(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(\"\", ...) did not panic")
		}
	}()
	agent.NewRegistry().RegisterKind(proto.SupportedAgentKind{Kind: "", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{})
}

func TestRegistryRegisterRejectsFactoriesForUnavailableRuntime(t *testing.T) {
	info := proto.SupportedAgentKind{Kind: "k", Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilitySupported})}
	executor := func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) { return nil, nil }
	registry := agent.NewRegistry()
	defer func() {
		if recover() == nil {
			t.Fatal("unavailable runtime registered factories")
		}
		if len(registry.SupportedAgentKinds()) != 0 {
			t.Fatal("rejected runtime changed registry")
		}
	}()
	registry.Register(agent.Declaration{Info: info}, agent.Runtime{Info: info, Executor: executor})
}

func TestRegistrySupportedAgentKindsReportsDescriptors(t *testing.T) {
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{
		Kind:      "fake_beta",
		Available: false,
		Version:   "missing",
		Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
			Streaming: proto.CapabilitySupported,
		}),
	}, harnessconfig.Configuration{})
	reg.RegisterKind(proto.SupportedAgentKind{
		Kind:      "fake_alpha",
		Available: true,
		Version:   "1.2.3",
		Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
			Streaming: proto.CapabilitySupported,
			Usage:     proto.CapabilitySupported,
			Resume:    proto.CapabilitySupported,
		}),
	}, harnessconfig.Configuration{})

	got := reg.SupportedAgentKinds()
	if len(got) != 2 {
		t.Fatalf("SupportedAgentKinds len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Kind != "fake_alpha" || got[1].Kind != "fake_beta" {
		t.Fatalf("SupportedAgentKinds sort = %#v, want fake_alpha then fake_beta", got)
	}
	if !got[0].Available || got[0].Version != "1.2.3" || !got[0].Capabilities.Usage.IsSupported() || !got[0].Capabilities.Resume.IsSupported() {
		t.Fatalf("fake_alpha descriptor not preserved: %#v", got[0])
	}
	if got[1].Available || got[1].Version != "missing" || !got[1].Capabilities.Streaming.IsSupported() {
		t.Fatalf("fake_beta descriptor not preserved: %#v", got[1])
	}
}

func TestRegistryExecutorRequiresExplicitRegistration(t *testing.T) {
	registry := agent.NewRegistry()
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "native", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{})
	if _, err := registry.ResolveExecutor("native"); err == nil {
		t.Fatal("kind registration implied reusable execution")
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
	registry.RegisterKind(proto.SupportedAgentKind{Kind: "native", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{})}, harnessconfig.Configuration{})
	if _, err := registry.ResolveExecutor("native"); err == nil {
		t.Fatal("replacing a kind retained its old executor capability")
	}
}

func TestRegistryRejectsEveryOmittedCapabilityBeforeReplacement(t *testing.T) {
	valid := prototest.Capabilities(proto.AgentKindCapabilities{})
	for i := 0; i < reflect.TypeOf(valid).NumField(); i++ {
		t.Run(reflect.TypeOf(valid).Field(i).Name, func(t *testing.T) {
			registry := agent.NewRegistry()
			original := proto.SupportedAgentKind{Kind: "fixture", Available: true, Capabilities: valid}
			registry.RegisterKind(original, harnessconfig.Configuration{})
			registry.RegisterExecutor("fixture", func(context.Context, proto.PromptRequestPayload) (agent.Executor, error) { return nil, nil })
			original.Capabilities.Preparation = proto.CapabilitySupported
			missing := valid
			reflect.ValueOf(&missing).Elem().Field(i).Set(reflect.ValueOf(proto.CapabilityUnspecified))
			func() {
				defer func() {
					if recover() == nil {
						t.Error("incomplete declaration registered")
					}
				}()
				registry.RegisterKind(proto.SupportedAgentKind{Kind: "fixture", Available: false, Capabilities: missing}, harnessconfig.Configuration{})
			}()
			if _, err := registry.ResolveExecutor("fixture"); err != nil || !reflect.DeepEqual(registry.SupportedAgentKinds(), []proto.SupportedAgentKind{original}) {
				t.Fatal("failed declaration changed registry")
			}
		})
	}
}
