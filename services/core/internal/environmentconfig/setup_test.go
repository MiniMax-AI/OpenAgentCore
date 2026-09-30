package environmentconfig

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func TestSetupReservesOpenAgentCoreNames(t *testing.T) {
	for _, name := range []string{"OAC_ADDR", "OAC_RUNTIME_HOME", "OAC_WEB_ORIGIN", "OAC_LOG_LEVEL", "OAC_DEV_HOME", "OAC_TEST_DATABASE_URL"} {
		if err := (Setup{Env: map[string]string{name: "value"}}).Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("reserved name %s accepted: %v", name, err)
		}
	}
	if err := (Setup{Env: map[string]string{"APPLICATION_VALUE": "ok"}}).Validate(); err != nil {
		t.Fatalf("ordinary application settings rejected: %v", err)
	}
}

func TestSetupValidate(t *testing.T) {
	for name, setup := range map[string]Setup{
		"invalid env name":      {Env: map[string]string{"1NAME": "value"}},
		"reserved PATH":         {Env: map[string]string{"PATH": "/bin"}},
		"reserved CODEX prefix": {Env: map[string]string{"CODEX_HOME": "/home"}},
		"NUL in env value":      {Env: map[string]string{"NAME": "a\x00b"}},
		"empty command":         {Commands: []SetupCommand{{Command: ""}}},
		"relative command cwd":  {Commands: []SetupCommand{{Command: "true", CWD: "workspace"}}},
		"option as package":     {Packages: v1.EnvironmentPackages{NPM: []string{"--global"}}},
		"empty package":         {Packages: v1.EnvironmentPackages{Python: []string{""}}},
	} {
		if err := setup.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	valid := Setup{Env: map[string]string{"NAME": "value"}, Commands: []SetupCommand{{Command: "true", CWD: "/workspace"}}, Packages: v1.EnvironmentPackages{NPM: []string{"is-number@7.0.0"}, Python: []string{"packaging==24.2"}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid setup rejected: %v", err)
	}
}

func TestSetupValidateRequestedAndInstalledSkills(t *testing.T) {
	reference := func(version string) Setup {
		return Setup{Skills: []Skill{{Metadata: SkillMetadata{Type: "skill_reference", SkillID: "skill_1", Version: version}}}}
	}
	for _, version := range []string{"", "latest", "3"} {
		if err := reference(version).Validate(); err != nil {
			t.Errorf("requested reference version %q rejected: %v", version, err)
		}
		if err := reference(version).ValidateInstalled(); !errors.Is(err, ErrInvalid) {
			t.Errorf("unresolved reference version %q installed: %v", version, err)
		}
	}
	if err := reference("03").Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("non-canonical requested version accepted: %v", err)
	}
	resolved := Setup{Skills: []Skill{{Metadata: SkillMetadata{Type: "skill_reference", Name: "proof", Description: "A proof.", SkillID: "skill_1", Version: "3"}, Archive: skillArchive(t)}}}
	if err := resolved.ValidateInstalled(); err != nil {
		t.Errorf("resolved reference rejected: %v", err)
	}
	if err := resolved.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("requested reference with resolved fields accepted: %v", err)
	}
	inline := Setup{Skills: []Skill{{Metadata: SkillMetadata{Type: "inline", Name: "proof", Description: "A proof."}, Archive: skillArchive(t)}}}
	if inline.Validate() != nil || inline.ValidateInstalled() != nil {
		t.Error("inline Skill rejected")
	}
	duplicate := Setup{Skills: append(inline.Skills, inline.Skills...)}
	if err := duplicate.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("duplicate Skill name accepted: %v", err)
	}
}

func TestSkillMetadataValidateInstalled(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata SkillMetadata
		valid    bool
	}{
		{"inline", SkillMetadata{Type: "inline", Name: "proof", Description: "A proof."}, true},
		{"reference", SkillMetadata{Type: "skill_reference", Name: "proof", Description: "A proof.", SkillID: "skill_1", Version: "1"}, true},
		{"missing name", SkillMetadata{Type: "inline", Description: "A proof."}, false},
		{"missing description", SkillMetadata{Type: "inline", Name: "proof"}, false},
		{"inline with version", SkillMetadata{Type: "inline", Name: "proof", Description: "A proof.", Version: "1"}, false},
		{"reference without ID", SkillMetadata{Type: "skill_reference", Name: "proof", Description: "A proof.", Version: "1"}, false},
		{"reference with latest", SkillMetadata{Type: "skill_reference", Name: "proof", Description: "A proof.", SkillID: "skill_1", Version: "latest"}, false},
		{"unknown type", SkillMetadata{Type: "remote", Name: "proof", Description: "A proof."}, false},
	} {
		err := test.metadata.ValidateInstalled()
		if test.valid && err != nil || !test.valid && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", test.name, err)
		}
	}
}

func skillArchive(t *testing.T) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.CreateHeader(&zip.FileHeader{Name: "proof/SKILL.md", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("---\nname: proof\ndescription: A proof.\n---\nBody.")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}
