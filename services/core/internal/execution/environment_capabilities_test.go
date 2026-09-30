package execution

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestSelfHostedCapabilitySourcesAreFrozenAndStrict(t *testing.T) {
	session := store.Session{ID: "session", TenantID: "tenant"}
	environment := store.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID,
		Configuration: []byte(`{"type":"self_hosted","workspace_directory":"/home/user/project","capability_directories":["/home/user/skills","/opt/plugins"]}`)}
	var request proto.PromptRequestPayload
	if err := (&Dispatcher{}).configurePreparedEnvironment(session, environment, store.ExecutionDevice{EnvironmentID: environment.ID}, &request); err != nil {
		t.Fatal(err)
	}
	local := request.LocalEnvironment
	if local.WorkspaceDirectory != "/home/user/project" || !local.Capabilities || local.ToolEnvironment || len(local.Skills) != 0 ||
		local.CapabilitySources == nil || !slices.Equal(local.CapabilitySources.Directories, []string{"/home/user/skills", "/opt/plugins"}) {
		t.Fatal("frozen source selections lost", local)
	}
	for _, configuration := range []string{
		`{"type":"self_hosted","workspace_directory":"/a/../b"}`,
		`{"type":"self_hosted","workspace_directory":"relative"}`,
		`{"type":"self_hosted","workspace_directory":"/work","capability_directories":["/a","/a"]}`,
	} {
		if LocalWorkspaceConfiguration([]byte(configuration)) {
			t.Fatal("invalid self-hosted shape accepted", configuration)
		}
	}
}

func TestSelfHostedPreparedPathsArePlatformNeutral(t *testing.T) {
	for _, directory := range []string{`C:\work`, `D:/skills`, `\\server\share\project`, `/Users/user/work`} {
		raw, _ := json.Marshal(map[string]any{"type": "self_hosted", "workspace_directory": directory, "capability_directories": []string{directory}})
		session := store.Session{ID: "session", TenantID: "tenant"}
		environment := store.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID, Configuration: raw}
		var request proto.PromptRequestPayload
		err := (&Dispatcher{}).configurePreparedEnvironment(session, environment, store.ExecutionDevice{EnvironmentID: environment.ID}, &request)
		if err != nil || request.LocalEnvironment.WorkspaceDirectory != directory || request.LocalEnvironment.CapabilitySources.Directories[0] != directory {
			t.Fatal("Core interpreted a Runtime source path", directory, err)
		}
	}
}
