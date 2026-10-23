package mcode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
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
	req.LocalEnvironment.Skills = []proto.InstalledSkill{{
		InstallationRoot: root, RelativeRoot: "plugin/skills/proof", PackageRoot: "plugin",
		Metadata: proto.SkillMetadata{Name: "proof", Description: "Read a marker"},
	}}
	for range 2 {
		opts, err := prepareWorkspaceOptions(testStateRoot(t), c, req)
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
				Skills, Tools []string
			}
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		selected := config.Agents["default"]
		// Skill names are not native IDs, so the selection stays open and no tool list carries "skill".
		if selected.Skills != nil || slices.Contains(selected.Tools, "skill") || !slices.Contains(selected.Tools, "bash") {
			t.Fatalf("native selection does not leave the installed Skill available: %+v", selected)
		}
	}
}
