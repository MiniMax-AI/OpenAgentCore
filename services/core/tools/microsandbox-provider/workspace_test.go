//go:build linux

package main

import (
	"encoding/json"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	"testing"
)

func TestEnvironmentQualificationRequiresExplicitOwnership(t *testing.T) {
	for _, fault := range []string{"match", "foreign path", "owned mount", "wrong guest", "missing object"} {
		t.Run(fault, func(t *testing.T) {
			config, actual := deploymentFixture()
			labels := workspaceLabels(fixtureWorkspace)
			actual["labels"] = labels
			mount := actual["mounts"].([]any)[0].(map[string]any)
			switch fault {
			case "foreign path":
				mount["host"] = "/foreign"
			case "owned mount":
				mount["type"] = "Owned"
			case "wrong guest":
				mount["guest"] = "/environment/workspace"
			case "missing object":
				delete(labels, workspaceObjectLabel)
			}
			raw, _ := json.Marshal(actual)
			err := qualifyConfiguration(config, wire.Compute{}, string(raw), false)
			if (err == nil) != (fault == "match") {
				t.Fatal(fault, err)
			}
		})
	}
}
func TestSnapshotWorkspaceIdentityAllowsReresolvedDirectory(t *testing.T) {
	workspace := &wire.WorkspaceDirectory{Path: "/old", ObjectID: "88888888-8888-4888-8888-888888888888"}
	labels := workspaceLabels(workspace)
	workspace.Path = "/new"
	if err := qualifyWorkspace(labels, workspace); err != nil {
		t.Fatal(err)
	}
	workspace.ObjectID = "99999999-9999-4999-8999-999999999999"
	if err := qualifyWorkspace(labels, workspace); err == nil {
		t.Fatal("foreign object accepted")
	}
}
