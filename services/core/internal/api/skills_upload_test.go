package api

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
)

func TestSkillMultipartPreservesPathsAndValidatesEntireUpload(t *testing.T) {
	manifest := []byte("---\nname: proof\ndescription: Verify an uploaded bundle.\n---\nRead scripts/proof.py.")
	type part struct {
		field, name string
		body        []byte
	}
	request := func(parts []part) *bytes.Buffer {
		var buffer bytes.Buffer
		w := multipart.NewWriter(&buffer)
		if err := w.SetBoundary("skill-proof-boundary"); err != nil {
			t.Fatal(err)
		}
		for _, entry := range parts {
			h := textproto.MIMEHeader{}
			disposition := `form-data; name="` + entry.field + `"`
			if entry.name != "" {
				disposition += `; filename="` + entry.name + `"`
			}
			h.Set("Content-Disposition", disposition)
			p, err := w.CreatePart(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return &buffer
	}
	parse := func(parts []part, version bool) ([]byte, bool, error) {
		r := httptest.NewRequest("POST", "/v1/skills", request(parts))
		r.Header.Set("Content-Type", "multipart/form-data; boundary=skill-proof-boundary")
		return readSkillUpload(r, version)
	}
	directory := []part{{"files[]", "proof/skill.md", manifest}, {"files[]", "proof/scripts/proof.py", []byte("print('proof')")}, {"default", "", []byte("true")}}
	body, defaultVersion, err := parse(directory, true)
	if err != nil || !defaultVersion {
		t.Fatal("directory upload", err)
	}
	meta, err := agentskill.Inspect(body)
	if err != nil {
		t.Fatal(err)
	}
	files, err := agentskill.Read(body, meta)
	if err != nil || len(files) != 2 || files[1].Path != "scripts/proof.py" {
		t.Fatal("directory paths lost", files, err)
	}
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	f, err := zw.Create("proof/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(manifest); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = parse([]part{{"files", "proof.zip", zipped.Bytes()}}, false); err != nil {
		t.Fatal("zip upload", err)
	}
	for _, parts := range [][]part{
		{{"files[]", "proof/SKILL.md", manifest}, {"files[]", "proof/../../private", []byte("escape")}},
		{{"files[]", "proof/SKILL.md", manifest}, {"files[]", "proof/SKILL.md", manifest}},
		{{"files", "proof.zip", zipped.Bytes()}, {"files[]", "proof/extra", []byte("mixed")}},
		{{"files[]", "proof/SKILL.md", manifest}, {"default", "", []byte("true")}, {"default", "", []byte("false")}},
		{{"files[]", "proof/SKILL.md", manifest}, {"unknown", "", []byte("ignored")}},
	} {
		if _, _, err = parse(parts, true); err == nil {
			t.Fatal("invalid tail committed", parts[1].field)
		}
	}
	if _, _, err = parse(directory, false); err == nil {
		t.Fatal("default accepted on Skill create")
	}
}
