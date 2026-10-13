//go:build linux

package main

import (
	"encoding/json"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	"testing"
)

func TestExternalEnvironmentQualificationRequiresExplicitOwnership(t *testing.T) {
	for _, fault := range []string{"match", "missing mode", "foreign path", "owned mount", "wrong guest", "missing object"} {
		t.Run(fault, func(t *testing.T) {
			config, actual := deploymentFixture()
			config.EnvironmentDiskMiB = 0
			config.ExternalWorkspace = true
			workspace := &wire.WorkspaceDirectory{Path: "/resolved/environment", ObjectID: "88888888-8888-4888-8888-888888888888"}
			labels := workspaceLabels(workspace)
			actual["labels"] = labels
			mount := map[string]any{"type": "Bind", "host": workspace.Path, "guest": "/environment"}
			actual["mounts"] = []any{mount}
			switch fault {
			case "missing mode":
				delete(labels, workspaceModeLabel)
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
	if err := qualifyWorkspace(labels, nil); err == nil {
		t.Fatal("external checkpoint accepted owned mode")
	}
	workspace.ObjectID = "99999999-9999-4999-8999-999999999999"
	if err := qualifyWorkspace(labels, workspace); err == nil {
		t.Fatal("foreign object accepted")
	}
}
