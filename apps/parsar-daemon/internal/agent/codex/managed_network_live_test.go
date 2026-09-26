//go:build linux

package codex

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	stdstrconv "strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentnetwork"
)

// Run inside the qualified Docker Runtime. This checks the real native process
// and managed requirements, not model execution or public protocol acceptance.
func TestManagedNetworkNativeLifecycle(t *testing.T) {
	if os.Getenv("OAC_TEST_CODEX_MANAGED_NETWORK_LIVE") != "1" {
		t.Skip("requires the qualified Docker Runtime and native Codex")
	}
	base, err := os.ReadFile(nativeManagedRequirements)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"close", "owner-cancel"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			plan := SessionPlan{Env: []string{"CODEX_HOME=" + home}}
			policy := agentnetwork.Policy{Access: "restricted", AllowedDomains: []string{"Example.com"}}
			if err := prepareManagedNetwork(&plan, policy); err != nil {
				t.Fatal(err)
			}
			cfg := JSONRPCConfig{Binary: "/usr/local/bin/codex", Env: append(os.Environ(), plan.Env...)}
			for _, entry := range plan.ExtraConfig {
				cfg.ExtraArgs = append(cfg.ExtraArgs, "-c", entry[0]+"="+entry[1])
			}
			if err := configureManagedNetworkProcess(&cfg, plan.managedRequirements); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			client := NewJSONRPCClient(cfg)
			defer client.Close()
			if _, err := client.Start(ctx, InitializeParams{
				ClientInfo:   InitializeClientInfo{Name: "network-lifecycle-test", Version: "1"},
				Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
			}); err != nil {
				t.Fatal(err)
			}
			requirements, err := client.Request(ctx, "configRequirements/read", nil)
			if err != nil || !bytes.Contains(requirements, []byte("example.com")) {
				t.Fatalf("native policy was not applied: %s: %v", requirements, err)
			}
			owned := networkNativeProcesses(t, home)
			if len(owned) == 0 {
				t.Fatal("native child was not observed")
			}
			if mode == "owner-cancel" {
				cancel()
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				survivors := networkNativeProcesses(t, home)
				if len(survivors) == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("native processes survived %s: %v", mode, survivors)
				}
				time.Sleep(20 * time.Millisecond)
			}
			if client.Alive() || client.cmd.ProcessState == nil {
				t.Fatal("RPC owner did not settle")
			}
		})
	}
	after, err := os.ReadFile(nativeManagedRequirements)
	if err != nil || !bytes.Equal(base, after) {
		t.Fatal("image requirements changed", err)
	}
}

func networkNativeProcesses(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, entry := range entries {
		pid, err := stdstrconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		env, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err == nil {
			for _, value := range strings.Split(string(env), "\x00") {
				if value == "CODEX_HOME="+home {
					found = append(found, entry.Name())
				}
			}
		}
	}
	return found
}
