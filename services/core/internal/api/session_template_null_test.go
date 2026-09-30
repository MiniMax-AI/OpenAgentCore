package api

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestTemplateNullSelectionRetainsInheritedCapabilitiesAndPolicy(t *testing.T) {
	template, err := decodeTemplateInput([]byte(`{"network":{"access":"restricted","allowed_domains":["example.com"]},"skills":[` + string(skillInput(t, "private-skill-marker")) + `],"plugins":[` + string(pluginInput(t)) + `],"capability_directories":["/workspace/generated"]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, fields                                string
		clearSkills, clearPlugins, clearDirectories bool
	}{
		{name: "omitted"},
		{name: "network null", fields: `,"network":null`},
		{name: "skills null", fields: `,"skills":null`},
		{name: "plugins null", fields: `,"plugins":null`},
		{name: "directories null", fields: `,"capability_directories":null`},
		{name: "all null", fields: `,"network": null ,"skills":null,"plugins":null,"capability_directories":null`},
		{name: "empty lists", fields: `,"network":null,"skills":[],"plugins":[],"capability_directories":[]`, clearSkills: true, clearPlugins: true, clearDirectories: true},
		{name: "clear only skills", fields: `,"skills":[],"plugins":null,"capability_directories":null`, clearSkills: true},
		{name: "clear only plugins", fields: `,"skills":null,"plugins":[],"capability_directories":null`, clearPlugins: true},
		{name: "clear only directories", fields: `,"skills":null,"plugins":null,"capability_directories":[]`, clearDirectories: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookup := &templateLookupStore{network: "restricted", domains: []string{"example.com"}, skills: template.Initialization.Skills, plugins: template.Initialization.Plugins, directories: template.Initialization.CapabilityDirectories}
			input := compositionRequest(t, test.fields)
			intent, err := sessionCreationRequest(input, nil)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(template.Initialization)
			h := templateHandler(t, lookup.ResolveEnvironmentTemplate)
			if err := h.resolveTemplateEnvironment(t.Context(), "tenant", &input); err != nil {
				t.Fatal(err)
			}
			if input.Environment.Network.Access != "restricted" || !reflect.DeepEqual(input.Environment.Network.AllowedDomains, lookup.domains) {
				t.Fatal("omitted/null network widened or lost template policy")
			}
			if test.clearSkills {
				if len(input.initialization.Skills) != 0 {
					t.Fatal("skills not cleared")
				}
			} else if !reflect.DeepEqual(input.initialization.Skills, template.Initialization.Skills) {
				t.Fatal("skills not inherited")
			}
			if test.clearPlugins {
				if len(input.initialization.Plugins) != 0 {
					t.Fatal("plugins not cleared")
				}
			} else if !reflect.DeepEqual(input.initialization.Plugins, template.Initialization.Plugins) {
				t.Fatal("plugins not inherited")
			}
			if test.clearDirectories {
				if len(input.initialization.CapabilityDirectories) != 0 {
					t.Fatal("directories not cleared")
				}
			} else if !reflect.DeepEqual(input.initialization.CapabilityDirectories, template.Initialization.CapabilityDirectories) {
				t.Fatal("directories not inherited")
			}
			if !reflect.DeepEqual(input.Environment.Skills, skillResponse(input.initialization.SkillMetadata())) || !reflect.DeepEqual(input.Environment.Plugins, pluginResponse(input.initialization.PluginMetadata())) || len(input.Environment.CapabilityDirectories) != len(input.initialization.CapabilityDirectories) {
				t.Fatal("public metadata does not match selected capabilities")
			}
			public, _ := json.Marshal(input.Environment)
			for _, secret := range []string{`"source"`, "private-skill-marker", "private-plugin"} {
				if bytes.Contains(public, []byte(secret)) {
					t.Fatal("private initialization leaked to public metadata")
				}
			}
			after, _ := json.Marshal(template.Initialization)
			afterIntent, _ := sessionCreationRequest(input, nil)
			if !bytes.Equal(before, after) || !bytes.Equal(intent, afterIntent) {
				t.Fatal("selection mutated template or caller intent")
			}
		})
	}
}

func TestTemplateNullSelectionDoesNotBypassValidation(t *testing.T) {
	for _, fields := range []string{`,"network":{}`, `,"network":[]`, `,"skills":[null]`, `,"plugins":[null]`, `,"capability_directories":[null]`, `,"capability_directories":["/private"]`} {
		if _, _, _, err := decodeTemplateEnvironment([]byte(`{"type":"openai_hosted","environment_template_id":"saved"` + fields + `}`)); err == nil {
			t.Fatal("invalid nonnull override accepted", fields)
		}
	}
	h := templateHandler(t, (&templateLookupStore{network: "disabled"}).ResolveEnvironmentTemplate)
	input := compositionRequest(t, `,"network":{"access":"enabled"},"skills":null,"plugins":null,"capability_directories":null`)
	if err := h.resolveTemplateEnvironment(t.Context(), "tenant", &input); err == nil {
		t.Fatal("capability null overrides bypassed network narrowing")
	}
}
