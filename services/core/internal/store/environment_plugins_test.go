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
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func TestPluginsFrozenInSession(t *testing.T) {
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
	setup := environmentconfig.Setup{Plugins: []environmentconfig.Plugin{{Metadata: agentplugin.Metadata{Type: "inline", Name: "plugin-proof", Description: "A proof."}, Archive: archive.Bytes()}}, CapabilityDirectories: []string{"/workspace/generated"}}
	tenant, foreign := uuid.NewString(), uuid.NewString()
	request := sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), Initialization: setup}
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
	frozen, err := s.ReadEnvironmentSetup(t.Context(), tenant, session.ID)
	if err != nil || !reflect.DeepEqual(frozen.Plugins, setup.Plugins) || !reflect.DeepEqual(frozen.CapabilityDirectories, setup.CapabilityDirectories) {
		t.Fatal("frozen snapshot changed", err)
	}
	retry, err := s.CreateSession(t.Context(), tenant, request)
	if err != nil || retry.ID != session.ID {
		t.Fatal("retry", err)
	}
	if _, err = s.ReadEnvironmentSetup(t.Context(), foreign, session.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign snapshot", err)
	}
}
