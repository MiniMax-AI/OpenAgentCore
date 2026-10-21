package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestDiscoveryAndRegistration(t *testing.T) {
	for _, selected := range []string{"codex", "mcode", "claude_sdk", ""} {
		t.Run(selected, func(t *testing.T) {
			runtimeRoot, stateRoot := t.TempDir(), t.TempDir()
			t.Setenv("OAC_RUNTIME_HOME", runtimeRoot)
			t.Setenv("OAC_RUNTIME_STATE_DIRECTORY", stateRoot)
			rc := &runContext{stdout: io.Discard, stderr: io.Discard}
			expected := []string{"claude_sdk", "codex", "mcode"}
			if selected != "" {
				rc.installedKinds = map[string]bool{selected: true}
				expected = []string{selected}
			}
			// Each probe waits for all selected probes to start, so a sequential
			// discovery fails here instead of passing slowly.
			var started sync.WaitGroup
			started.Add(len(expected))
			all := make(chan struct{})
			go func() { started.Wait(); close(all) }()
			declarations := append([]agent.Declaration(nil), harnessDeclarations...)
			for i := range declarations {
				declarations[i].Discover = func(_ context.Context, options agent.DiscoveryOptions, info proto.SupportedAgentKind) *agent.Runtime {
					if options.RuntimeRoot != runtimeRoot || options.StateRoot != stateRoot {
						t.Error("discovery lost explicit directory context")
					}
					started.Done()
					select {
					case <-all:
					case <-time.After(5 * time.Second):
						t.Error("preflights did not run concurrently")
					}
					fmt.Fprintln(options.Stdout, info.Kind)
					info.Available = true
					info.Version = "test"
					return &agent.Runtime{Info: info}
				}
			}
			var stdout strings.Builder
			rc.stdout = &stdout
			discovery, err := discoverAgentCLIs(t.Context(), rc, "default", declarations)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Fields(stdout.String()); !reflect.DeepEqual(got, expected) {
				t.Fatalf("preflight output %v, want %v", got, expected)
			}
			registry := agent.NewRegistry()
			registerAgentKinds(registry, discovery)
			kinds := registry.SupportedAgentKinds()
			if len(kinds) != len(expected) {
				t.Fatal(kinds)
			}
			for _, info := range kinds {
				if !info.Available || info.Version != "test" {
					t.Fatal(info)
				}
			}
		})
	}
}
func TestDiscoveryUnavailableAndCancelled(t *testing.T) {
	declarations := append([]agent.Declaration(nil), harnessDeclarations...)
	for i := range declarations {
		declarations[i].Discover = func(_ context.Context, _ agent.DiscoveryOptions, info proto.SupportedAgentKind) *agent.Runtime {
			return &agent.Runtime{Info: info}
		}
	}
	rc := &runContext{stdout: io.Discard, stderr: io.Discard}
	if _, err := discoverAgentCLIs(t.Context(), rc, "default", declarations); err == nil {
		t.Fatal("unavailable runtimes admitted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := discoverAgentCLIs(ctx, rc, "default", declarations); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	declarations[0].Discover = func(context.Context, agent.DiscoveryOptions, proto.SupportedAgentKind) *agent.Runtime { return nil }
	discovery, _ := discoverAgentCLIs(t.Context(), rc, "default", declarations)
	if len(discovery) != 2 {
		t.Fatal("unconfigured adapter retained")
	}
}
