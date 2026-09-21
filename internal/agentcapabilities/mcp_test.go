package agentcapabilities

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMCPOnlyInstalledPackageSurvivesSourceDeletion(t *testing.T) {
	workspace, installed := openTestRoot(t), openTestRoot(t)
	writeWorkspace(t, workspace, "plugin/.codex-plugin/plugin.json", []byte(`{"name":"native-tools","description":"Package tools.","mcpServers":"./.mcp.json"}`))
	writeWorkspace(t, workspace, "plugin/.mcp.json", []byte(`{"mcpServers":{"proof":{"command":"python3","args":["proof.py"],"env_vars":["PLUGIN_TOKEN"]}}}`))
	writeWorkspace(t, workspace, "plugin/proof.py", []byte("# Preserved server resource.\n"))
	if err := Finalize(workspace, installed, Input{Directories: []string{"/workspace/plugin"}}); err != nil {
		t.Fatal(err)
	}
	before, err := Load(installed)
	if err != nil || len(before.Skills) != 0 || len(before.MCP) != 1 || before.MCP[0].PackageRoot != "directories/0" || before.MCP[0].Server.Name != "proof" {
		t.Fatalf("MCP-only installed package lost: %+v %v", before, err)
	}
	body, err := installed.ReadFile(ManifestName)
	if err != nil || strings.Contains(string(body), "PLUGIN_TOKEN") || strings.Contains(string(body), "command") {
		t.Fatalf("manifest duplicates server configuration: %s %v", body, err)
	}
	if err := workspace.RemoveAll("plugin"); err != nil {
		t.Fatal(err)
	}
	after, err := Load(installed)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("load changed frozen MCP configuration: %+v %v", after, err)
	}
	if err := installed.Remove("directories/0/.mcp.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(installed); err == nil {
		t.Fatal("missing retained declaration accepted")
	}
}

func TestInstalledMCPManifestRejectsInvalidPackageRoots(t *testing.T) {
	for _, roots := range [][]string{{"../private"}, {"/private"}, {"plugins/0", "plugins/0"}} {
		body, err := json.Marshal(Manifest{Version: 1, Plugins: roots})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeManifest(body); err == nil {
			t.Errorf("invalid package roots accepted: %v", roots)
		}
	}
}
