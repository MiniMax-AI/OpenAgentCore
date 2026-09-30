package mcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
)

func TestWorkspaceSkillsUseSelectedSnapshotAndNativeLoader(t *testing.T) {
	c, req, _ := workspaceFixture(t)
	root := t.TempDir()
	path := filepath.Join(root, "plugin", "skills", "proof")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	const body = "---\nname: proof\ndescription: Read a marker\n---\nSKILL_ONLY_729\n"
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	req.LocalEnvironment.CapabilityRoot = root
	req.LocalEnvironment.Skills = []agentcapabilities.InstalledSkill{{
		InstallationRoot: root, RelativeRoot: "plugin/skills/proof", PackageRoot: "plugin",
		Metadata: agentskill.Metadata{Name: "proof", Description: "Read a marker"},
	}}
	for range 2 {
		opts, err := prepareWorkspaceOptions(t.Context(), c, req)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := os.ReadFile(filepath.Join(opts.DataDir, "skills", "proof", "SKILL.md"))
		if err != nil || string(loaded) != body {
			t.Fatal("native Skill path does not resolve to installed content", err)
		}
		raw, err := os.ReadFile(filepath.Join(opts.DataDir, "config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Agents map[string]struct {
				Skills, Tools, BuiltinTools []string
			}
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		selected := config.Agents["default"]
		if !reflect.DeepEqual(selected.Skills, []string{"proof"}) ||
			!reflect.DeepEqual(selected.Tools, []string{"skill"}) ||
			!reflect.DeepEqual(selected.BuiltinTools, []string{"skill"}) {
			t.Fatalf("native selection lost the installed Skill: %+v", selected)
		}
	}
}
