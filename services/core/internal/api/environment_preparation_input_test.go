package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

func TestPreparationExtensionUsesOneConfiguration(t *testing.T) {
	setup := `{"env":{"EXPLICIT":"private"},"packages":{"npm":["is-number@7.0.0"]},"setup_commands":[{"command":"touch proof","cwd":"/workspace"}],"files":[{"type":"inline","path":"/workspace/input.txt","data":"aGVsbG8="}],"capability_directories":["/workspace/generated"]}`
	var snapshots []sessionRequest
	for _, environment := range []string{`{"type":"openai_hosted"}`, `{"type":"self_hosted","workspace_directory":"/home/user/work"}`} {
		var request decodedSessionRequest
		if err := json.Unmarshal([]byte(`{"environment":`+environment+`,"x_agents_core":{"environment":`+setup+`}}`), &request); err != nil {
			t.Fatal(err)
		}
		got, err := request.validated()
		if err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, got)
	}
	if !reflect.DeepEqual(snapshots[0].initialization, snapshots[1].initialization) || !reflect.DeepEqual(snapshots[0].initialFiles, snapshots[1].initialFiles) {
		t.Fatal("placement changed preparation")
	}
	if snapshots[1].Environment.WorkspaceDirectory != "/home/user/work" {
		t.Fatal("user workspace changed")
	}
	raw, err := json.Marshal(snapshots[1].Environment)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if fields["env"] != nil || fields["setup_commands"] != nil {
		t.Fatal("confidential setup entered public snapshot")
	}
}

func TestPreparationExtensionRejectsAmbiguousAndPlacementInput(t *testing.T) {
	for _, input := range []string{
		`{"environment":{"type":"none"},"x_agents_core":{"environment":{"skills":[]}}}`,
		`{"environment":{"type":"self_hosted","workspace_directory":"/work"},"x_agents_core":{"environment":{"network":{"access":"disabled"}}}}`,
		`{"environment":{"type":"self_hosted","workspace_directory":"/work"},"x_agents_core":{"environment":{"workspace_directory":"/other"}}}`,
		`{"environment":{"type":"self_hosted","workspace_directory":"/work","capability_directories":[]},"x_agents_core":{"environment":{"capability_directories":[]}}}`,
		`{"environment":{"type":"openai_hosted","env":null},"x_agents_core":{"environment":{"env":{"KEY":"value"}}}}`,
		`{"environment":{"type":"self_hosted","workspace_directory":"/work"},"x_agents_core":{"environment":{"packages":{"system":[]}}}}`,
	} {
		var request decodedSessionRequest
		if err := json.Unmarshal([]byte(input), &request); err != nil {
			t.Fatal(err)
		}
		if _, err := request.validated(); err == nil {
			t.Fatal("invalid extension accepted", input)
		}
	}
}

func TestSelfHostedTemplateUsesExtension(t *testing.T) {
	var request decodedSessionRequest
	_ = json.Unmarshal([]byte(`{"environment":{"type":"self_hosted","workspace_directory":"/work"},"x_agents_core":{"environment":{"environment_template_id":"template-id"}}}`), &request)
	value, err := request.validated()
	if err != nil || value.templateID != "template-id" || value.Environment.Type != "self_hosted" {
		t.Fatal(value.templateID, err)
	}
	if _, _, _, err := decodeTemplateEnvironment(json.RawMessage(`{"type":"self_hosted","workspace_directory":"/work","environment_template_id":"template-id"}`)); err == nil {
		t.Fatal("extension presented as an official field")
	}
}

func TestPreparationTemplateSharedAcrossPlacements(t *testing.T) {
	for _, fields := range []string{"", `,"env":{"SHARED":"replacement"},"files":[],"packages":{"npm":[]}`, `,"env":null,"setup_commands":null`} {
		hosted := compositionRequest(t, fields)
		var request decodedSessionRequest
		raw := `{"environment":{"type":"self_hosted","workspace_directory":"/work"},"x_agents_core":{"environment":{"environment_template_id":"saved"` + fields + `}}}`
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			t.Fatal(err)
		}
		own, err := request.validated()
		if err != nil {
			t.Fatal(err)
		}
		h := templateHandler(t, compositionFixture().ResolveEnvironmentTemplate)
		for _, input := range []*sessionRequest{&hosted, &own} {
			if err := h.resolveTemplateEnvironment(t.Context(), "tenant", input); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(hosted.initialization, own.initialization) || !reflect.DeepEqual(hosted.initialFiles, own.initialFiles) {
			t.Fatal("template preparation changed with placement")
		}
	}
}

func TestPreparationExtensionCreationIntentDoesNotDuplicateFiles(t *testing.T) {
	data := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), 4<<20))
	for _, placement := range []string{"openai_hosted", "self_hosted"} {
		t.Run(placement, func(t *testing.T) {
			environment := map[string]any{"type": placement}
			if placement == "self_hosted" {
				environment["workspace_directory"] = "/work"
			}
			raw, err := json.Marshal(map[string]any{"environment": environment, "x_agents_core": map[string]any{"environment": map[string]any{"files": []any{
				map[string]any{"type": "inline", "path": "/workspace/a.txt", "data": data},
				map[string]any{"type": "inline", "path": "/workspace/b.txt", "data": data},
			}}}})
			if err != nil {
				t.Fatal(err)
			}
			var request decodedSessionRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Fatal(err)
			}
			input, err := request.validated()
			if err != nil {
				t.Fatal(err)
			}
			intent, err := sessionCreationRequest(input, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Store hashes creation intent under the same 16 MiB request budget.
			if len(intent) > 16<<20 || bytes.Count(intent, []byte(data)) != 2 {
				t.Fatalf("legal file request was duplicated: input=%d, intent=%d", len(raw), len(intent))
			}
			if len(input.initialFiles) != 2 {
				t.Fatal("files no longer reach Runtime preparation")
			}
			input.XAgentsCore.Environment = json.RawMessage(`{"files":[]}`)
			changed, err := sessionCreationRequest(input, nil)
			if err != nil || bytes.Equal(intent, changed) {
				t.Fatal("changed caller intent lost retry identity", err)
			}
		})
	}
}
