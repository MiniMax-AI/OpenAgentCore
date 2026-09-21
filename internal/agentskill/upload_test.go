package agentskill

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestInspectUsesInstallationValidation(t *testing.T) {
	archive := func(names ...string) []byte {
		t.Helper()
		var buffer bytes.Buffer
		w := zip.NewWriter(&buffer)
		for _, name := range names {
			f, err := w.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.Write([]byte("---\nname: proof\ndescription: Run a proof.\n---\nInstructions")); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes()
	}
	data := archive("proof/skill.md")
	metadata, err := Inspect(data)
	if err != nil || metadata.Name != "proof" {
		t.Fatal("manifest discovery", err)
	}
	files, err := Read(data, metadata)
	if err != nil || len(files) != 1 || files[0].Path != "SKILL.md" {
		t.Fatal("canonical native manifest", files, err)
	}
	for _, names := range [][]string{
		{"proof/SKILL.md", "proof/skill.md"},
		{"proof/SKILL.md", "proof/../../secret"},
		{"proof/SKILL.md", "other/file"},
		{"proof/missing.md"},
	} {
		if _, err := Inspect(archive(names...)); err == nil {
			t.Fatal("invalid upload accepted", names)
		}
	}
}
