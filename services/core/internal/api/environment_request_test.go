package api

import (
	"encoding/json"
	"testing"
)

func TestSelfHostedPathsUseCommonInitialization(t *testing.T) {
	raw := json.RawMessage(`{"type":"self_hosted","workspace_directory":"/home/user/work","capability_directories":["/opt/skills","/home/user/plugins"]}`)
	request, err := (decodedSessionRequest{Environment: raw}).validated()
	if err != nil {
		t.Fatal(err)
	}
	if len(request.initialization.CapabilityDirectories) != len(request.Environment.CapabilityDirectories) || request.Environment.WorkspaceDirectory != "/home/user/work" || len(request.Environment.CapabilityDirectories) != 2 {
		t.Fatal("local selections missing from common initialization", request.initialization, request.Environment)
	}
	for _, raw := range []string{
		`{"type":"self_hosted","workspace_directory":"/a/../b"}`,
		`{"type":"self_hosted","workspace_directory":"/work/"}`,
		`{"type":"self_hosted","workspace_directory":"/work","capability_directories":["relative"]}`,
		`{"type":"self_hosted","workspace_directory":"/work","capability_directories":["/a","/a"]}`,
		`{"type":"self_hosted","workspace_directory":"/work","plugins":[]}`,
		`{"type":"self_hosted","workspace_directory":"/work","skills":[]}`,
	} {
		if _, err := (decodedSessionRequest{Environment: json.RawMessage(raw)}).validated(); err == nil {
			t.Fatal("invalid local input accepted", raw)
		}
	}
}

func TestSelfHostedSourcePathsArePlatformNeutral(t *testing.T) {
	for _, directory := range []string{`C:\work`, `D:/skills`, `\\server\share\project`, `/Users/user/work`} {
		raw, _ := json.Marshal(map[string]any{"type": "self_hosted", "workspace_directory": directory, "capability_directories": []string{directory}})
		request, err := (decodedSessionRequest{Environment: raw}).validated()
		if err != nil || request.Environment.WorkspaceDirectory != directory || len(request.initialization.CapabilityDirectories) != len(request.Environment.CapabilityDirectories) {
			t.Fatal("Core interpreted a Runtime source path", directory, err)
		}
	}
}
