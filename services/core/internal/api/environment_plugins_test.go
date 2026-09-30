package api

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func pluginInput(t *testing.T) json.RawMessage {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for path, body := range map[string]string{
		".codex-plugin/plugin.json": `{"name":"proof-plugin","description":"Two Skills.","skills":["./skills"]}`,
		"skills/alpha/SKILL.md":     "---\nname: alpha\ndescription: Alpha proof.\n---\nprivate-plugin-canary",
		"skills/beta/SKILL.md":      "---\nname: beta\ndescription: Beta proof.\n---\nUse ../../shared/data.txt",
		"shared/data.txt":           "private-plugin-resource",
	} {
		f, err := writer.Create("proof/" + path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"type": "inline", "name": "proof-plugin", "description": "Two Skills.", "source": map[string]string{"type": "base64", "media_type": "application/zip", "data": base64.StdEncoding.EncodeToString(archive.Bytes())}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPluginsSharedParsingConfidentialMetadataAndOverrides(t *testing.T) {
	plugin := pluginInput(t)
	raw := []byte(`{"plugins":[` + string(plugin) + `],"capability_directories":["/workspace/generated"]}`)
	template, err := decodeTemplateInput(raw)
	if err != nil || !template.SetPlugins || !template.SetDirectories || len(template.Initialization.Plugins) != 1 {
		t.Fatal("template", err)
	}
	var decoded decodedSessionRequest
	if err = json.Unmarshal([]byte(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted",`+string(raw[1:])+`}`), &decoded); err != nil {
		t.Fatal(err)
	}
	input, err := decoded.validated()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input.initialization.Plugins, template.Initialization.Plugins) {
		t.Fatal("different installation inputs")
	}
	cfg, err := resolve(input, "tenant", "key", nil)
	if err != nil || bytes.Contains(cfg, []byte(`"source"`)) || bytes.Contains(cfg, []byte("private-plugin")) {
		t.Fatal("unsafe snapshot", err)
	}
	var snapshot struct {
		Environment json.RawMessage `json:"environment"`
	}
	if err = json.Unmarshal(cfg, &snapshot); err != nil {
		t.Fatal(err)
	}
	stored, err := storedEnvironment(snapshot.Environment)
	if err != nil || len(stored.Plugins) != 1 || len(stored.CapabilityDirectories) != 1 {
		t.Fatal("metadata", err)
	}
	env := store.Environment{ID: "environment", Status: "connected", Configuration: snapshot.Environment}
	if response, err := environmentResponse(env); err != nil || len(response.Plugins) != 1 {
		t.Fatal("environment response", err)
	}
	if response, err := hostedSessionEnvironment(env); err != nil || len(*response.Plugins) != 1 || len(*response.CapabilityDirectories) != 1 {
		t.Fatal("session response", err)
	}
	lookup := &templateLookupStore{network: "enabled", plugins: template.Initialization.Plugins, directories: template.Initialization.CapabilityDirectories}
	h := Handler{store: lookup}
	for _, override := range []string{"", `,"plugins":null,"capability_directories":null`, `,"plugins":[],"capability_directories":[]`} {
		if err = json.Unmarshal([]byte(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","environment_template_id":"template"`+override+`}}`), &decoded); err != nil {
			t.Fatal(err)
		}
		in, err := decoded.validated()
		if err != nil {
			t.Fatal(err)
		}
		intent, err := sessionCreationRequest(in, nil)
		if err != nil || !bytes.Contains(intent, []byte("template")) {
			t.Fatal("intent", err)
		}
		if err = h.resolveTemplateEnvironment(t.Context(), "tenant", &in); err != nil {
			t.Fatal(err)
		}
		want := 1
		if strings.Contains(override, "[]") {
			want = 0
		}
		if len(in.initialization.Plugins) != want || len(in.initialization.CapabilityDirectories) != want || len(in.Environment.Plugins) != want {
			t.Fatal("inheritance/replacement")
		}
	}
	for _, directory := range []string{`null`, `"/private"`, `"/workspace/../private"`, `"relative"`} {
		if _, err := decodeTemplateInput([]byte(`{"capability_directories":[` + directory + `]}`)); err == nil {
			t.Fatal("unsafe directory")
		}
	}
	bad := strings.Replace(string(plugin), `"name":"proof-plugin"`, `"name":"mismatch"`, 1)
	if _, err := decodeEnvironmentPlugins([]byte("[" + bad + "]")); err == nil {
		t.Fatal("mismatched identity")
	}
	if _, err := decodeEnvironmentPlugins([]byte("[" + string(plugin) + "," + string(plugin) + "]")); err == nil {
		t.Fatal("duplicate identity")
	}
}
