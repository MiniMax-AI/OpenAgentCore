package api

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/go-chi/chi/v5"
)

func skillZip(t *testing.T, name, description string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create("proof/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("---\nname: " + name + "\ndescription: " + description + "\n---\nbody")); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// skillServer serves the Skill routes over HTTP, which the bounded transfers need.
func skillServer(t *testing.T, fake *fakeSkills) *httptest.Server {
	t.Helper()
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminTenantContextKey{}, "tenant")))
		})
	})
	(&Handler{Dependencies: Dependencies{Skills: fake}}).registerSkillRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

func TestSkillUploadFieldErrors(t *testing.T) {
	type part struct {
		field, filename string
		body            []byte
	}
	good := skillZip(t, "proof", "Verify an upload.")
	for _, test := range []struct {
		name    string
		version bool
		parts   []part
		want    string
	}{
		{"no files", false, nil, `"message":"Missing required parameter: 'files'.","type":"invalid_request_error","code":"invalid_request_error","param":"files"`},
		{"default on create", false, []part{{"files", "p.zip", good}, {"default", "", []byte("true")}}, `"message":"Unknown parameter: 'default'.","type":"invalid_request_error","code":"invalid_request_error","param":"default"`},
		{"bad default", true, []part{{"files", "p.zip", good}, {"default", "", []byte("yes")}}, `"code":"invalid_request_error","param":"default"`},
		{"bad bundle", false, []part{{"files", "p.zip", []byte("not a zip")}}, `"type":"invalid_request_error","code":"invalid_value","param":"files"`},
		{"underscore name", false, []part{{"files", "p.zip", skillZip(t, "my_skill", "Verify an upload.")}}, `"message":"The Skill name must not contain '_'.","type":"invalid_request_error","code":"invalid_value","param":"files"`},
		{"long description", false, []part{{"files", "p.zip", skillZip(t, "proof", strings.Repeat("d", 1025))}}, `"message":"The Skill description must be at most 1024 characters.","type":"invalid_request_error","code":"invalid_value","param":"files"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var buffer bytes.Buffer
			writer := multipart.NewWriter(&buffer)
			for _, entry := range test.parts {
				header := textproto.MIMEHeader{}
				disposition := `form-data; name="` + entry.field + `"`
				if entry.filename != "" {
					disposition += `; filename="` + entry.filename + `"`
				}
				header.Set("Content-Disposition", disposition)
				p, err := writer.CreatePart(header)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = p.Write(entry.body)
			}
			_ = writer.Close()
			path := "/v1/skills"
			if test.version {
				path += "/skill_x/versions"
			}
			response, err := http.Post(skillServer(t, &fakeSkills{t: t}).URL+path, writer.FormDataContentType(), &buffer)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), test.want) {
				t.Fatalf("%d %s", response.StatusCode, body)
			}
		})
	}
}

func TestSkillUploadAcceptsCapitalizedDefault(t *testing.T) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	file, _ := writer.CreateFormFile("files", "p.zip")
	_, _ = file.Write(skillZip(t, "proof", "Verify an upload."))
	_ = writer.WriteField("default", "True")
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/v1/skills/skill_x/versions", &buffer)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if _, makeDefault, err := readSkillUpload(request, true); err != nil || !makeDefault {
		t.Fatal(makeDefault, err)
	}
}

func TestSkillUpdateErrors(t *testing.T) {
	fake := &fakeSkills{t: t, setDefaultVersion: func(context.Context, skills.SetDefaultVersion) (skills.Skill, error) {
		return skills.Skill{}, skills.ErrVersionNotFound
	}}
	router := chi.NewRouter()
	router.Post("/v1/skills/{skill_id}", (&Handler{Dependencies: Dependencies{Skills: fake}}).updateSkill)
	for _, test := range []struct{ name, body, want string }{
		{"unknown key", `{"default_version":"1","x":1}`, `"message":"Unknown parameter: 'x'.","type":"invalid_request_error","code":"invalid_request_error","param":"x"`},
		{"missing", `{}`, `"message":"Missing required parameter: 'default_version'.","type":"invalid_request_error","code":"invalid_request_error","param":"default_version"`},
		{"wrong type", `{"default_version":1}`, `"message":"Invalid type for 'default_version': expected a string, but got an integer instead.","type":"invalid_request_error","code":"invalid_request_error","param":"default_version"`},
		{"missing version", `{"default_version":"9"}`, `"message":"Resource not found.","type":"invalid_request_error","code":null,"param":"default_version"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/skills/skill_x", strings.NewReader(test.body))
			request = request.WithContext(context.WithValue(request.Context(), adminTenantContextKey{}, "tenant"))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if !strings.Contains(response.Body.String(), test.want) || (test.name == "missing version") != (response.Code == http.StatusNotFound) {
				t.Fatalf("%d %s", response.Code, response.Body)
			}
		})
	}
}

func TestSkillContentDownloadHeaders(t *testing.T) {
	fake := &fakeSkills{t: t, readDefaultVersion: func(context.Context, skills.ReadDefaultVersion) (skills.Content, error) {
		return skills.Content{Version: skills.Version{Name: "proof", Version: 3}, Archive: []byte("zip")}, nil
	}}
	response, err := http.Get(skillServer(t, fake).URL + "/v1/skills/skill_x/content")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "application/zip" || response.Header.Get("Content-Disposition") != `attachment; filename=proof-3.zip` {
		t.Fatal(response.Header)
	}
}
