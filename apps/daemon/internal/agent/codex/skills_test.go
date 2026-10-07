package codex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
)

// TestRegisterSkillsChecksWhatCodexLoaded checks that the installed Skill's
// root is registered and that Codex's own listing must show exactly that
// Skill there, without native dependencies.
func TestRegisterSkillsChecksWhatCodexLoaded(t *testing.T) {
	root := filepath.Join("/managed", "skills", "review")
	manifest := filepath.Join(root, "SKILL.md")
	skill := map[string]any{"name": "review", "path": manifest, "scope": "user", "enabled": true}
	nested := map[string]any{"name": "other", "path": filepath.Join(root, "references", "other", "SKILL.md"), "scope": "user", "enabled": true}
	dependent := map[string]any{"name": "review", "path": manifest, "dependencies": map[string]any{"tools": []any{map[string]string{"type": "mcp", "value": "docs"}}}}
	system := map[string]any{"name": "imagegen", "path": filepath.Join("/home", ".codex", "skills", ".system", "imagegen", "SKILL.md"), "scope": "system", "enabled": true}
	registered, _ := json.Marshal(SkillsExtraRootsSetParams{ExtraRoots: []string{root}})
	for name, c := range map[string]struct {
		skills, errors []any
		accepted       bool
	}{
		"the Skill":              {[]any{skill, system}, nil, true},
		"a nested Skill":         {[]any{skill, nested}, nil, false},
		"native dependencies":    {[]any{dependent}, nil, false},
		"a Skill that failed":    {[]any{system}, []any{map[string]string{"message": "invalid", "path": manifest}}, false},
		"a Skill that is absent": {[]any{system}, nil, false},
	} {
		client, server, cleanup := NewTestClient()
		result := make(chan error, 1)
		go func() {
			result <- registerSkills(context.Background(), client.JSONRPCClient, "/workspace",
				[]agentcapabilities.InstalledSkill{{InstallationRoot: "/managed", RelativeRoot: "skills/review"}})
		}()
		decoder := json.NewDecoder(server.FromClient)
		for _, method := range []string{"skills/extraRoots/set", "skills/list"} {
			var request struct {
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := decoder.Decode(&request); err != nil || request.Method != method {
				t.Fatalf("%s: request %s, %v; want %s", name, request.Method, err, method)
			}
			var reply any = map[string]any{}
			switch method {
			case "skills/extraRoots/set":
				if string(request.Params) != string(registered) {
					t.Fatalf("%s: params %s", name, request.Params)
				}
			case "skills/list":
				reply = map[string]any{"data": []any{map[string]any{"cwd": "/workspace", "skills": c.skills, "errors": c.errors}}}
			}
			response, _ := json.Marshal(map[string]any{"id": request.ID, "result": reply})
			if _, err := server.ToClient.Write(append(response, '\n')); err != nil {
				t.Fatal(err)
			}
		}
		if err := <-result; (err == nil) != c.accepted {
			t.Errorf("%s: registerSkills = %v, want accepted %v", name, err, c.accepted)
		}
		cleanup()
	}
}
