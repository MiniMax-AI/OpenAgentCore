package store

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/google/uuid"
)

func TestPluginsEncryptedTemplateAndFrozenSession(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{23}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for path, body := range map[string]string{
		".codex-plugin/plugin.json": `{"name":"plugin-proof","description":"A proof.","skills":"./skills"}`,
		"skills/proof/SKILL.md":     "---\nname: proof\ndescription: A proof.\n---\nplugin-private-canary",
		"shared/data.txt":           "plugin-private-resource",
	} {
		f, err := writer.CreateHeader(&zip.FileHeader{Name: "proof/" + path, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	setup := EnvironmentSetup{Plugins: []EnvironmentPlugin{{Metadata: agentplugin.Metadata{Type: "inline", Name: "plugin-proof", Description: "A proof."}, Archive: archive.Bytes()}}, CapabilityDirectories: []string{"/workspace/generated"}}
	tenant, foreign := uuid.NewString(), uuid.NewString()
	template, err := s.CreateEnvironmentTemplate(t.Context(), tenant, EnvironmentTemplateInput{SetPlugins: true, SetDirectories: true, Initialization: setup})
	if err != nil {
		t.Fatal(err)
	}
	public, err := New(pool).GetEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || len(public.Plugins) != 1 || !public.Initialization.Empty() || !reflect.DeepEqual(public.CapabilityDirectories, setup.CapabilityDirectories) {
		t.Fatal("safe metadata without decryption", err)
	}
	page, err := New(pool).ListEnvironmentTemplates(t.Context(), tenant, "", 20, false)
	if err != nil || len(page.Templates) != 1 || len(page.Templates[0].Plugins) != 1 {
		t.Fatal("list", err)
	}
	var metadata, encrypted []byte
	if err = pool.QueryRow(t.Context(), "SELECT plugins,plugin_contents FROM environment_templates WHERE id=$1", template.ID).Scan(&metadata, &encrypted); err != nil || len(encrypted) == 0 || bytes.Contains(metadata, []byte("private")) || bytes.Contains(encrypted, []byte("private")) {
		t.Fatal("plaintext storage", err)
	}
	resolved, _, err := s.ResolveEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || !reflect.DeepEqual(resolved.Initialization.Plugins, setup.Plugins) {
		t.Fatal("resolution", err)
	}
	for _, read := range []func() error{
		func() error { _, e := s.GetEnvironmentTemplate(t.Context(), foreign, template.ID); return e },
		func() error { _, _, e := s.ResolveEnvironmentTemplate(t.Context(), foreign, template.ID); return e },
		func() error {
			_, e := s.UpdateEnvironmentTemplate(t.Context(), foreign, template.ID, EnvironmentTemplateInput{SetPlugins: true})
			return e
		},
	} {
		if err := read(); !errors.Is(err, ErrNotFound) {
			t.Fatal("tenant isolation", err)
		}
	}
	request := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), Initialization: resolved.Initialization}
	session, err := s.CreateSession(t.Context(), tenant, request)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Environment struct {
			Plugins     []agentplugin.Metadata `json:"plugins"`
			Directories []string               `json:"capability_directories"`
		} `json:"environment"`
	}
	if err = json.Unmarshal(session.Configuration, &cfg); err != nil || len(cfg.Environment.Plugins) != 1 || !reflect.DeepEqual(cfg.Environment.Directories, setup.CapabilityDirectories) {
		t.Fatal("frozen public metadata", err)
	}
	name := "renamed"
	if _, err = s.UpdateEnvironmentTemplate(t.Context(), tenant, template.ID, EnvironmentTemplateInput{SetName: true, Name: &name}); err != nil {
		t.Fatal(err)
	}
	preserved, _, err := s.ResolveEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || !reflect.DeepEqual(preserved.Initialization.Plugins, setup.Plugins) || !reflect.DeepEqual(preserved.Initialization.CapabilityDirectories, setup.CapabilityDirectories) {
		t.Fatal("unrelated update changed installation", err)
	}
	if _, err = s.UpdateEnvironmentTemplate(t.Context(), tenant, template.ID, EnvironmentTemplateInput{SetPlugins: true, SetDirectories: true}); err != nil {
		t.Fatal(err)
	}
	cleared, _, err := s.ResolveEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || len(cleared.Initialization.Plugins) != 0 || len(cleared.CapabilityDirectories) != 0 {
		t.Fatal("clear", err)
	}
	if _, err = s.DeleteEnvironmentTemplate(t.Context(), tenant, template.ID); err != nil {
		t.Fatal(err)
	}
	frozen, err := s.ReadEnvironmentSetup(t.Context(), tenant, session.ID)
	if err != nil || !reflect.DeepEqual(frozen.Plugins, setup.Plugins) || !reflect.DeepEqual(frozen.CapabilityDirectories, setup.CapabilityDirectories) {
		t.Fatal("frozen snapshot changed", err)
	}
	retry, err := s.CreateSession(t.Context(), tenant, request)
	if err != nil || retry.ID != session.ID {
		t.Fatal("retry", err)
	}
	if _, err = s.ReadEnvironmentSetup(t.Context(), foreign, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign snapshot", err)
	}
}
