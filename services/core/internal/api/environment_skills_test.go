package api

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func skillInput(t *testing.T, body string) json.RawMessage {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("proof/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("---\nname: proof\ndescription: A proof.\n---\n" + body)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"type": "inline", "name": "proof", "description": "A proof.", "source": map[string]string{"type": "base64", "media_type": "application/zip", "data": base64.StdEncoding.EncodeToString(archive.Bytes())}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSkillReferenceParsingInheritanceAndReplacement(t *testing.T) {
	lookup := &templateLookupStore{network: "enabled", skills: []store.EnvironmentSkill{{Metadata: store.EnvironmentSkillMetadata{Type: "skill_reference", SkillID: "skill-template", Version: "latest"}}}}
	h := Handler{store: lookup}
	for _, fields := range []string{"", `,"skills":[]`, `,"skills":[{"type":"skill_reference","skill_id":"skill-override","version":"2"}]`} {
		var decoded decodedSessionRequest
		if err := json.Unmarshal([]byte(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","environment_template_id":"template"`+fields+`}}`), &decoded); err != nil {
			t.Fatal(err)
		}
		input, err := decoded.validated()
		if err != nil {
			t.Fatal(err)
		}
		intent, err := sessionCreationRequest(input, nil)
		if err != nil || len(intent) == 0 {
			t.Fatal("missing unresolved retry intent", err)
		}
		if err = h.resolveTemplateEnvironment(t.Context(), "tenant", &input); err != nil {
			t.Fatal(err)
		}
		switch fields {
		case "":
			if len(input.initialization.Skills) != 1 || input.initialization.Skills[0].Metadata != lookup.skills[0].Metadata {
				t.Fatal("reference inheritance")
			}
		case `,"skills":[]`:
			if len(input.initialization.Skills) != 0 {
				t.Fatal("explicit empty list did not replace")
			}
		default:
			if len(input.initialization.Skills) != 1 || input.initialization.Skills[0].Metadata.SkillID != "skill-override" || input.initialization.Skills[0].Metadata.Version != "2" {
				t.Fatal("reference replacement")
			}
		}
	}
	for _, version := range []string{"", `,"version":"latest"`, `,"version":"1"`} {
		raw := []byte(`{"type":"openai_hosted","skills":[{"type":"skill_reference","skill_id":"skill-owned"` + version + `}]}`)
		var decoded decodedSessionRequest
		if err := json.Unmarshal(append(append([]byte(`{"agent":{"model":"test"},"environment":`), raw...), '}'), &decoded); err != nil {
			t.Fatal(err)
		}
		input, err := decoded.validated()
		if err != nil {
			t.Fatal(err)
		}
		intent, err := sessionCreationRequest(input, nil)
		if err != nil || !bytes.Contains(intent, []byte("skill-owned")) {
			t.Fatal("inline reference lost retry intent", err)
		}
	}
	for _, version := range []string{`1`, `""`, `"0"`} {
		if _, err := decodeEnvironmentSkills([]byte(`[{"type":"skill_reference","skill_id":"skill-owned","version":` + version + `}]`)); err == nil {
			t.Fatal("invalid or unconfirmed selector accepted")
		}
	}
}

func TestInlineSkillsSharedParsingSnapshotAndIntent(t *testing.T) {
	skill := skillInput(t, "private-skill-canary")
	raw := append(append([]byte(`{"skills":[`), skill...), []byte(`]}`)...)
	template, err := decodeTemplateInput(raw)
	if err != nil || !template.SetSkills || len(template.Initialization.Skills) != 1 {
		t.Fatal("template", err)
	}
	environment := append([]byte(`{"type":"openai_hosted",`), raw[1:]...)
	decode := func(agent string, environment []byte) sessionRequest {
		var request decodedSessionRequest
		if err := json.Unmarshal(append(append([]byte(`{`+agent+`,"environment":`), environment...), '}'), &request); err != nil {
			t.Fatal(err)
		}
		input, err := request.validated()
		if err != nil {
			t.Fatal(err)
		}
		return input
	}
	inline := decode(`"agent":{"model":"test"}`, environment)
	if !bytes.Equal(inline.initialization.Skills[0].Archive, template.Initialization.Skills[0].Archive) {
		t.Fatal("inline/template differ")
	}
	configuration, err := resolve(inline, "tenant", "key", nil)
	if err != nil || bytes.Contains(configuration, []byte(`"source"`)) || bytes.Contains(configuration, []byte(`"archive"`)) {
		t.Fatal("confidential snapshot", err)
	}
	public, err := storedEnvironment(mustEnvironment(t, configuration))
	if err != nil || len(public.Skills) != 1 || !bytes.Contains(public.Skills[0], []byte(`"name":"proof"`)) {
		t.Fatal("metadata", err)
	}
	intent, err := sessionCreationRequest(decode(`"agent_id":"saved"`, environment), nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := append(append([]byte(`{"type":"openai_hosted","skills":[`), skillInput(t, "different")...), []byte(`]}`)...)
	other, err := sessionCreationRequest(decode(`"agent_id":"saved"`, changed), nil)
	if err != nil || bytes.Equal(intent, other) {
		t.Fatal("archive omitted from retry identity", err)
	}
	for _, clearing := range []string{`{"skills":null}`, `{"skills":[]}`} {
		input, err := decodeTemplateInput([]byte(clearing))
		if err != nil || !input.SetSkills || !input.Initialization.Empty() {
			t.Fatal("clear", err)
		}
	}
	for _, invalid := range []string{`{"skills":[null]}`, `{"skills":[{"type":"skill_reference","skill_id":""}]}`, `{"skills":[{"type":"inline","name":"proof","description":"A proof.","source":{"type":"base64","media_type":"application/zip","data":"invalid"}}]}`} {
		if _, err := decodeTemplateInput([]byte(invalid)); err == nil {
			t.Fatal("invalid or unsupported skill accepted")
		}
	}
	duplicate := append(append(append(append([]byte(`{"skills":[`), skill...), ','), skill...), []byte(`]}`)...)
	if _, err := decodeTemplateInput(duplicate); err == nil {
		t.Fatal("duplicate skill accepted")
	}
}

func mustEnvironment(t *testing.T, configuration []byte) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(configuration, &fields); err != nil {
		t.Fatal(err)
	}
	return fields["environment"]
}
