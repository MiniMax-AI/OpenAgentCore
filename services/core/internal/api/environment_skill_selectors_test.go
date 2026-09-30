package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestSkillReferenceNullableSelectorAdmissionAndTemplateProjection(t *testing.T) {
	for _, test := range []struct {
		name, field, selector string
		publicVersion         any
	}{
		{name: "omitted"},
		{name: "null", field: `,"version":null`},
		{name: "null whitespace", field: `,"version": null `},
		{name: "latest", field: `,"version":"latest"`, selector: "latest", publicVersion: "latest"},
		{name: "exact", field: `,"version":"2"`, selector: "2", publicVersion: "2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			skills := `[{"type":"skill_reference","skill_id":"skill-owned"` + test.field + `}]`
			template, err := decodeTemplateInput([]byte(`{"skills":` + skills + `}`))
			if err != nil || !template.SetSkills || len(template.Setup.Skills) != 1 {
				t.Fatalf("template admission: %+v %v", template, err)
			}
			want := environmentconfig.Skill{Metadata: environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: "skill-owned", Version: test.selector}}
			if !reflect.DeepEqual(template.Setup.Skills[0], want) {
				t.Fatalf("unresolved selector changed: %+v", template.Setup.Skills[0])
			}
			public := templateResponse(environmenttemplates.Template{Skills: template.Setup.SkillMetadata()})
			var reference map[string]any
			if len(public.Skills) != 1 || json.Unmarshal(public.Skills[0], &reference) != nil {
				t.Fatalf("template projection: %+v", public.Skills)
			}
			if !reflect.DeepEqual(reference, map[string]any{"type": "skill_reference", "skill_id": "skill-owned", "version": test.publicVersion}) {
				t.Fatalf("template reference fields: %v", reference)
			}
			for _, templateField := range []string{"", `,"environment_template_id":"template-owned"`} {
				var request decodedSessionRequest
				body := `{"agent":{"model":"test"},"environment":{"type":"openai_hosted"` + templateField + `,"skills":` + skills + `}}`
				if err := json.Unmarshal([]byte(body), &request); err != nil {
					t.Fatal(err)
				}
				input, err := request.validated()
				if err != nil || !reflect.DeepEqual(input.initialization.Skills, template.Setup.Skills) {
					t.Fatalf("Session admission differs from Template: %+v %v", input.initialization.Skills, err)
				}
			}
		})
	}
}

func TestSkillReferenceNullDoesNotWidenOtherSelectors(t *testing.T) {
	for _, version := range []string{`""`, `"default"`, `"LATEST"`, `"01"`, `"0"`, `"-1"`, `1`, `true`, `{}`, `[]`} {
		if _, err := decodeEnvironmentSkills([]byte(`[{"type":"skill_reference","skill_id":"skill-owned","version":` + version + `}]`)); err == nil {
			t.Fatalf("invalid selector accepted: %s", version)
		}
	}
}

func TestInstalledSkillReferenceRequiresConcreteVersion(t *testing.T) {
	metadata := environmentconfig.SkillMetadata{Type: "skill_reference", SkillID: "skill-owned", Version: "2", Name: "proof", Description: "A proof."}
	public := skillResponse([]environmentconfig.SkillMetadata{metadata})
	var reference map[string]any
	if len(public) != 1 || json.Unmarshal(public[0], &reference) != nil {
		t.Fatalf("installed projection: %s", public)
	}
	want := map[string]any{"type": "skill_reference", "skill_id": "skill-owned", "version": "2", "name": "proof", "description": "A proof."}
	if !reflect.DeepEqual(reference, want) {
		t.Fatalf("installed metadata changed: %v", reference)
	}
	for _, selector := range []string{`"2"`, `null`, `"latest"`} {
		raw := json.RawMessage(`{"type":"openai_hosted","skills":[{"type":"skill_reference","skill_id":"skill-owned","version":` + selector + `,"name":"proof","description":"A proof."}]}`)
		result, err := environmentResponse(store.Environment{ID: "environment-owned", Status: "pending", Configuration: raw})
		if selector != `"2"` {
			if err == nil {
				t.Fatalf("unresolved installed selector accepted: %s", selector)
			}
			continue
		}
		if err != nil || len(result.Skills) != 1 || json.Unmarshal(result.Skills[0], &reference) != nil || !reflect.DeepEqual(reference, want) {
			t.Fatalf("stored environment projection: %+v %v", result, err)
		}
	}
}
