package api

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

type compositionTemplateStore struct {
	ResourceStore
	template store.EnvironmentTemplate
	files    []store.InitialFile
}

func (s *compositionTemplateStore) ResolveEnvironmentTemplate(context.Context, string, string) (store.EnvironmentTemplate, []store.InitialFile, error) {
	return s.template, s.files, nil
}

func compositionRequest(t *testing.T, fields string) sessionRequest {
	t.Helper()
	var request decodedSessionRequest
	body := `{"agent":{"model":"test"},"environment":{"type":"openai_hosted","environment_template_id":"saved"` + fields + `}}`
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatal(err)
	}
	input, err := request.validated()
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func compositionFixture() *compositionTemplateStore {
	return &compositionTemplateStore{
		template: store.EnvironmentTemplate{NetworkAccess: "enabled", Initialization: store.EnvironmentSetup{
			Env:      map[string]string{"TEMPLATE": "private-template-env", "SHARED": "private-old-value"},
			Commands: []store.SetupCommand{{Command: "printf private-template-command"}},
			Packages: v1.EnvironmentPackages{NPM: []string{"semver@7.7.2"}, Python: []string{"packaging==25.0"}},
		}},
		files: []store.InitialFile{{Type: "inline", Path: "/workspace/template", Data: []byte("private-template-bytes")}},
	}
}

func TestTemplateInlineCompositionRules(t *testing.T) {
	for _, test := range []struct {
		name, fields string
		change       func(*store.EnvironmentSetup, *[]store.InitialFile)
	}{
		{name: "omitted"},
		{name: "null", fields: `,"env":null,"setup_commands":null,"files":null,"packages":null`},
		{name: "empty objects", fields: `,"env":{},"packages":{}`},
		{name: "manager null", fields: `,"packages":{"npm":null,"python": null }`},
		{name: "clear lists", fields: `,"setup_commands":[],"files":[],"packages":{"npm":[],"python":[]}`, change: func(s *store.EnvironmentSetup, f *[]store.InitialFile) {
			s.Commands, *f = nil, nil
			s.Packages = v1.EnvironmentPackages{}
		}},
		{name: "populated", fields: `,"env":{"SHARED":"private-inline-env","INLINE":"private-new-value"},"setup_commands":[{"command":"printf private-first"},{"command":"printf private-second","cwd":"/workspace"}],"files":[{"type":"inline","path":"/workspace/inline","data":"cHJpdmF0ZS1pbmxpbmUtYnl0ZXM="}],"packages":{"python":["idna==3.10"],"npm":[]}`, change: func(s *store.EnvironmentSetup, f *[]store.InitialFile) {
			s.Env["SHARED"], s.Env["INLINE"] = "private-inline-env", "private-new-value"
			s.Commands = []store.SetupCommand{{Command: "printf private-first"}, {Command: "printf private-second", CWD: "/workspace"}}
			*f = []store.InitialFile{{Type: "inline", Path: "/workspace/inline", Data: []byte("private-inline-bytes")}}
			s.Packages.NPM, s.Packages.Python = nil, []string{"idna==3.10"}
		}},
		{name: "mixed managers", fields: `,"packages":{"npm":null,"python":["idna==3.10"]}`, change: func(s *store.EnvironmentSetup, _ *[]store.InitialFile) {
			s.Packages.Python = []string{"idna==3.10"}
		}},
		{name: "overlapping path replaces whole list", fields: `,"files":[{"type":"inline","path":"/workspace/template","data":"bmV3"}]`, change: func(_ *store.EnvironmentSetup, f *[]store.InitialFile) {
			*f = []store.InitialFile{{Type: "inline", Path: "/workspace/template", Data: []byte("new")}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookup, expected := compositionFixture(), compositionFixture()
			wantSetup, wantFiles := expected.template.Initialization, expected.files
			if test.change != nil {
				test.change(&wantSetup, &wantFiles)
			}
			input := compositionRequest(t, test.fields)
			intent, err := sessionCreationRequest(input, nil)
			if err != nil {
				t.Fatal(err)
			}
			beforeTemplate, _ := json.Marshal(lookup.template)
			beforeFiles, _ := json.Marshal(lookup.files)
			h := Handler{store: lookup}
			if err := h.resolveTemplateEnvironment(t.Context(), "tenant", &input); err != nil {
				t.Fatal(err)
			}
			// JSON omitempty normalizes nil/empty private slices without erasing public lists.
			actual, _ := json.Marshal(input.initialization)
			want, _ := json.Marshal(wantSetup)
			if !bytes.Equal(actual, want) || !reflect.DeepEqual(initialFileResponse(input.initialFiles), initialFileResponse(wantFiles)) {
				t.Fatalf("wrong composed initialization: %s; files=%+v", actual, input.initialFiles)
			}
			for i := range wantFiles {
				if !bytes.Equal(input.initialFiles[i].Data, wantFiles[i].Data) {
					t.Fatal("composed file bytes changed")
				}
			}
			if !reflect.DeepEqual(*input.Environment.Packages, wantSetup.PackageMetadata()) || !reflect.DeepEqual(input.Environment.Files, initialFileResponse(wantFiles)) {
				t.Fatal("public metadata differs from effective initialization")
			}
			public, _ := json.Marshal(input.Environment)
			for _, private := range []string{"private-", `"env"`, `"setup_commands"`, `"data"`, "environment_template_id"} {
				if bytes.Contains(public, []byte(private)) {
					t.Fatalf("confidential input in public metadata: %s", public)
				}
			}
			afterIntent, _ := sessionCreationRequest(input, nil)
			afterTemplate, _ := json.Marshal(lookup.template)
			afterFiles, _ := json.Marshal(lookup.files)
			if !bytes.Equal(intent, afterIntent) || !bytes.Equal(beforeTemplate, afterTemplate) || !bytes.Equal(beforeFiles, afterFiles) {
				t.Fatal("composition changed caller intent or template")
			}
		})
	}
}

func TestTemplateCompositionDoesNotAliasChangedInputs(t *testing.T) {
	for _, fields := range []string{"", `,"env":{"INLINE":"value"},"setup_commands":[{"command":"true"}],"files":[{"type":"inline","path":"/workspace/inline","data":""}],"packages":{"npm":["idna"]}`} {
		lookup := compositionFixture()
		input := compositionRequest(t, fields)
		originalSetup, originalFiles := input.initialization, input.initialFiles
		beforeInline, _ := json.Marshal(originalSetup)
		beforeInlineFiles, _ := json.Marshal(originalFiles)
		beforeTemplate, _ := json.Marshal(lookup.template)
		beforeFiles, _ := json.Marshal(lookup.files)
		h := Handler{store: lookup}
		if err := h.resolveTemplateEnvironment(t.Context(), "tenant", &input); err != nil {
			t.Fatal(err)
		}
		input.initialization.Env["SHARED"] = "changed"
		input.initialization.Commands[0].Command = "changed"
		input.initialization.Packages.NPM[0] = "changed"
		input.initialFiles[0].Path = "/workspace/changed"
		afterInline, _ := json.Marshal(originalSetup)
		afterInlineFiles, _ := json.Marshal(originalFiles)
		afterTemplate, _ := json.Marshal(lookup.template)
		afterFiles, _ := json.Marshal(lookup.files)
		if !bytes.Equal(beforeInline, afterInline) || !bytes.Equal(beforeInlineFiles, afterInlineFiles) || !bytes.Equal(beforeTemplate, afterTemplate) || !bytes.Equal(beforeFiles, afterFiles) {
			t.Fatal("composed maps/slices alias caller or template")
		}
	}
}

func TestTemplateCompositionRevalidatesCombinedSetupLimit(t *testing.T) {
	for _, field := range []string{"env", "packages"} {
		lookup := compositionFixture()
		lookup.template.Initialization.Env = map[string]string{"TEMPLATE": strings.Repeat("a", 300<<10)}
		var override any = map[string]any{"INLINE": strings.Repeat("b", 300<<10)}
		if field == "packages" {
			override = map[string]any{"python": []string{strings.Repeat("b", 300<<10)}}
		}
		raw, _ := json.Marshal(override)
		input := compositionRequest(t, `,"`+field+`":`+string(raw))
		if lookup.template.Initialization.Validate() != nil || input.initialization.Validate() != nil {
			t.Fatal("each side must be valid independently")
		}
		h := Handler{store: lookup}
		if err := h.resolveTemplateEnvironment(t.Context(), "tenant", &input); err == nil {
			t.Fatalf("combined setup limit bypassed for %s", field)
		}
	}
}

func TestTemplateInlineCompositionRetainsFieldValidation(t *testing.T) {
	for _, fields := range []string{
		`,"env":{"PATH":"private-canary"}`, `,"env":{"OAC_RUNTIME_HOME":"private-canary"}`, `,"env":{"KEY":null}`,
		`,"setup_commands":[{"command":"true","cwd":"relative"}]`, `,"packages":{"python":[null]}`, `,"packages":{"npm":["--unsafe"]}`,
		`,"files":[{"type":"inline","path":"/workspace/../private","data":""}]`,
		`,"files":[{"type":"inline","path":"/workspace/a","data":""},{"type":"inline","path":"/workspace/a","data":""}]`,
	} {
		var request decodedSessionRequest
		body := `{"agent":{"model":"test"},"environment":{"type":"openai_hosted","environment_template_id":"saved"` + fields + `}}`
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Fatal(err)
		}
		if _, err := request.validated(); err == nil {
			t.Fatalf("invalid inline field accepted: %s", fields)
		}
	}
}

func TestTemplateFilesReplacementDoesNotCombineCounts(t *testing.T) {
	lookup := compositionFixture()
	files := make([]store.InitialFile, 30)
	for i := range files {
		files[i] = store.InitialFile{Type: "inline", Path: "/workspace/" + strings.Repeat("a", i+1)}
	}
	lookup.files = files
	wire := make([]map[string]string, len(files))
	for i, file := range files {
		wire[i] = map[string]string{"type": "inline", "path": file.Path, "data": ""}
	}
	raw, _ := json.Marshal(wire)
	input := compositionRequest(t, `,"files":`+string(raw))
	h := Handler{store: lookup}
	if err := h.resolveTemplateEnvironment(t.Context(), "tenant", &input); err != nil || len(input.initialFiles) != 30 {
		t.Fatalf("file lists were combined: count=%d err=%v", len(input.initialFiles), err)
	}
}
