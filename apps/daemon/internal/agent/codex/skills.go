package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
)

// registerSkills registers each installed Skill's root as an extra root and
// checks, in what Codex itself loaded, that each root holds exactly its Skill.
// Codex scans extra roots recursively, so a nested SKILL.md would add a native
// Skill of its own, and a native dependency declaration may start MCP
// installation outside the workspace tools; both are refused. The paths are as
// the Harness sees them, so this works for a view's sandbox paths too.
func registerSkills(ctx context.Context, rpc *JSONRPCClient, cwd string, skills []agentcapabilities.InstalledSkill) error {
	roots := make([]string, 0, len(skills))
	for _, skill := range skills {
		roots = append(roots, localworkspace.SkillPath(skill))
	}
	if _, err := rpc.Request(ctx, "skills/extraRoots/set", SkillsExtraRootsSetParams{ExtraRoots: roots}); err != nil {
		return fmt.Errorf("codex: register skill roots: %w", err)
	}
	raw, err := rpc.Request(ctx, "skills/list", SkillsListParams{Cwds: []string{cwd}, ForceReload: true})
	var listed SkillsListResponse
	if err == nil {
		err = json.Unmarshal(raw, &listed)
	}
	if err != nil {
		return fmt.Errorf("codex: list skills: %w", err)
	}
	within := func(name string) string {
		for _, root := range roots {
			if strings.HasPrefix(name, root+string(filepath.Separator)) {
				return root
			}
		}
		return ""
	}
	loaded := map[string]bool{}
	for _, entry := range listed.Data {
		for _, failure := range entry.Errors {
			if within(failure.Path) != "" {
				return errors.New("codex: an installed Skill failed to load")
			}
		}
		for _, skill := range entry.Skills {
			root := within(skill.Path)
			switch {
			case root == "":
			case skill.Path != filepath.Join(root, "SKILL.md"):
				return errors.New("codex: nested native Skills are unsupported")
			case skill.Dependencies != nil:
				return errors.New("codex: native Skill dependencies are unsupported")
			default:
				loaded[root] = true
			}
		}
	}
	if len(loaded) != len(roots) {
		return errors.New("codex: an installed Skill did not load")
	}
	return nil
}
