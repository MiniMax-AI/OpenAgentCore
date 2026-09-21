package codex

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentnetwork"
)

func TestManagedNetworkPreservesImageRequirements(t *testing.T) {
	base, err := os.ReadFile("../../../../../services/agents-api/deploy/codex/requirements.toml")
	if err != nil {
		t.Fatal(err)
	}
	policy := agentnetwork.Policy{Access: "restricted", AllowedDomains: []string{"Example.com", "api.example.com", "example.com"}}
	result, err := managedNetworkRequirements(base, policy)
	if err != nil {
		t.Fatal(err)
	}
	var original, merged map[string]any
	if _, err = toml.Decode(string(base), &original); err != nil {
		t.Fatal(err)
	}
	if _, err = toml.Decode(string(result), &merged); err != nil {
		t.Fatal(err)
	}
	network := merged["experimental_network"].(map[string]any)
	delete(merged, "experimental_network")
	if !reflect.DeepEqual(merged, original) {
		t.Fatal("native filesystem/approval/hooks changed")
	}
	for _, flag := range []string{"enabled", "managed_allowed_domains_only"} {
		if network[flag] != true {
			t.Fatal("missing ceiling", flag)
		}
	}
	for _, flag := range []string{"allow_upstream_proxy", "dangerously_allow_all_unix_sockets", "allow_local_binding"} {
		if network[flag] != false {
			t.Fatal("network escape enabled", flag)
		}
	}
	want := map[string]any{"example.com": "allow", "api.example.com": "allow"}
	if !reflect.DeepEqual(network["domains"], want) {
		t.Fatal("exact domains changed", network["domains"])
	}
	if _, err := managedNetworkRequirements(result, policy); err == nil {
		t.Fatal("image ceiling overwritten")
	}
}

func TestManagedNetworkWrapsTheExistingRPCCommand(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "managed-network.toml")
	cfg := JSONRPCConfig{Binary: binary, ExtraArgs: []string{"-c", "features.network_proxy=true"}, Env: []string{"HOME=/private"}, Cwd: "/workspace"}
	if err := configureManagedNetworkProcess(&cfg, path); err != nil {
		t.Fatal(err)
	}
	if cfg.Binary != "/usr/bin/bwrap" || cfg.Cwd != "/workspace" || !reflect.DeepEqual(cfg.Env, []string{"HOME=/private"}) {
		t.Fatal("RPC ownership/configuration changed")
	}
	want := []string{"--die-with-parent", "--unshare-pid", "--bind", "/", "/", "--dev-bind", "/dev", "/dev", "--proc", "/proc", "--ro-bind", path, nativeManagedRequirements, binary, "-c", "features.network_proxy=true"}
	if !reflect.DeepEqual(cfg.ExtraArgs, want) {
		t.Fatal(cfg.ExtraArgs)
	}
}
