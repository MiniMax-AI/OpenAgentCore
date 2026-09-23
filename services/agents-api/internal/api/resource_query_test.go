package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// missingResourceStore reports every resource as missing, recording the tenant
// each lookup used.
type missingResourceStore struct {
	ResourceStore
	tenants []string
}

func (s *missingResourceStore) missing(tenant string) error {
	s.tenants = append(s.tenants, tenant)
	return store.ErrNotFound
}

func (s *missingResourceStore) GetAgent(_ context.Context, tenant, _ string) (store.SavedAgent, error) {
	return store.SavedAgent{}, s.missing(tenant)
}

func (s *missingResourceStore) DeleteAgent(_ context.Context, tenant, _ string) (string, error) {
	return "", s.missing(tenant)
}

func (s *missingResourceStore) UpdateAgent(_ context.Context, tenant, _ string, _ store.UpdateAgentInput) (store.SavedAgent, error) {
	return store.SavedAgent{}, s.missing(tenant)
}

func (s *missingResourceStore) GetSession(_ context.Context, tenant, _ string) (store.Session, error) {
	return store.Session{}, s.missing(tenant)
}

func (s *missingResourceStore) DeleteSession(_ context.Context, tenant, _ string) error {
	return s.missing(tenant)
}

func (s *missingResourceStore) UpdateSessionMetadata(_ context.Context, tenant, _ string, _ map[string]string) (store.Session, error) {
	return store.Session{}, s.missing(tenant)
}

func (s *missingResourceStore) GetEnvironmentTemplate(_ context.Context, tenant, _ string) (store.EnvironmentTemplate, error) {
	return store.EnvironmentTemplate{}, s.missing(tenant)
}

func (s *missingResourceStore) UpdateEnvironmentTemplate(_ context.Context, tenant, _ string, _ store.EnvironmentTemplateInput) (store.EnvironmentTemplate, error) {
	return store.EnvironmentTemplate{}, s.missing(tenant)
}

func (s *missingResourceStore) DeleteEnvironmentTemplate(_ context.Context, tenant, _ string) (string, error) {
	return "", s.missing(tenant)
}

// twoTenantHandler authenticates "test-api-key" as the owner and "foreign-key" as
// another project.
func twoTenantHandler(t *testing.T, s ResourceStore, options ...Option) (http.Handler, string, string) {
	t.Helper()
	owner, foreign := uuid.NewString(), uuid.NewString()
	auth, err := NewAuthenticator([]APIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "owner", TokenSHA256: device.HashCredential("test-api-key"), TenantID: owner},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "foreign", TokenSHA256: device.HashCredential("foreign-key"), TenantID: foreign},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(s, auth, "codex", options...)
	if err != nil {
		t.Fatal(err)
	}
	return h, owner, foreign
}

// Unknown query keys on single-resource routes are ignored: a missing or foreign
// resource keeps the same not-found response and authenticated tenant.
func TestSingleResourceRoutesIgnoreUnknownQueryKeys(t *testing.T) {
	id := uuid.NewString()
	for _, route := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/agents/" + id, ""},
		{http.MethodPost, "/v1/agents/" + id, `{"name":"Renamed"}`},
		{http.MethodDelete, "/v1/agents/" + id, ""},
		{http.MethodGet, "/v1/agents/sessions/" + id, ""},
		{http.MethodPost, "/v1/agents/sessions/" + id, `{"metadata":{"key":"value"}}`},
		{http.MethodDelete, "/v1/agents/sessions/" + id, ""},
		{http.MethodGet, "/v1/agents/environments/templates/" + id, ""},
		{http.MethodPost, "/v1/agents/environments/templates/" + id, `{"name":"Renamed"}`},
		{http.MethodDelete, "/v1/agents/environments/templates/" + id, ""},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			s := &missingResourceStore{}
			h, tenant, _ := twoTenantHandler(t, s)
			var bodies []string
			for _, query := range []string{"", "?tenant_id=foreign&include=files&unknown=1&unknown=2"} {
				r := httptest.NewRequest(route.method, route.path+query, strings.NewReader(route.body))
				r.Header.Set("Authorization", "Bearer test-api-key")
				r.Header.Set("OpenAI-Beta", "agents=v1")
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusNotFound {
					t.Fatalf("%s: %d %s", query, w.Code, w.Body)
				}
				bodies = append(bodies, w.Body.String())
			}
			if bodies[0] != bodies[1] || len(s.tenants) != 2 || s.tenants[0] != tenant || s.tenants[1] != tenant {
				t.Fatalf("query changed the response or tenant: %v %v", bodies, s.tenants)
			}
		})
	}
}

// missingSkillStore reports every Skill as missing and records list parameters.
type missingSkillStore struct {
	SkillStore
	tenants []string
	limit   int
	hasMore bool
}

func (s *missingSkillStore) GetSkill(_ context.Context, tenant, _ string) (store.Skill, error) {
	s.tenants = append(s.tenants, tenant)
	return store.Skill{}, store.ErrNotFound
}

func (s *missingSkillStore) DeleteSkill(_ context.Context, tenant, _ string) error {
	s.tenants = append(s.tenants, tenant)
	return store.ErrNotFound
}

func (s *missingSkillStore) GetSkillVersion(_ context.Context, tenant, _, _ string) (store.SkillVersion, error) {
	s.tenants = append(s.tenants, tenant)
	return store.SkillVersion{}, store.ErrNotFound
}

func (s *missingSkillStore) ListSkills(_ context.Context, tenant, _ string, limit int, _ bool) (store.SkillPage, error) {
	s.tenants, s.limit = append(s.tenants, tenant), limit
	return store.SkillPage{HasMore: s.hasMore}, nil
}

func (s *missingSkillStore) ListSkillVersions(_ context.Context, tenant, _, _ string, limit int, _ bool) (store.SkillVersionPage, error) {
	s.tenants, s.limit = append(s.tenants, tenant), limit
	return store.SkillVersionPage{HasMore: s.hasMore}, nil
}

func skillQueryHandler(t *testing.T, s SkillStore) (http.Handler, string) {
	t.Helper()
	h, tenant, _ := twoTenantHandler(t, &missingResourceStore{}, WithSkills(s))
	return h, tenant
}

func skillQueryRequest(h http.Handler, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer test-api-key")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSkillResourceRoutesIgnoreUnknownQueryKeys(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/v1/skills/skill_missing"},
		{http.MethodDelete, "/v1/skills/skill_missing"},
		{http.MethodGet, "/v1/skills/skill_missing/versions/1"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			s := &missingSkillStore{}
			h, tenant := skillQueryHandler(t, s)
			plain := skillQueryRequest(h, route.method, route.path)
			query := skillQueryRequest(h, route.method, route.path+"?tenant_id=foreign&limit=5&unknown=1")
			if plain.Code != http.StatusNotFound || query.Code != plain.Code || query.Body.String() != plain.Body.String() || len(s.tenants) != 2 || s.tenants[1] != tenant {
				t.Fatalf("query changed the response: %d %s / %d %s %v", plain.Code, plain.Body, query.Code, query.Body, s.tenants)
			}
		})
	}
}

func TestSkillListLimitZeroReturnsEmptyPage(t *testing.T) {
	for _, path := range []string{"/v1/skills", "/v1/skills/skill_example/versions"} {
		for _, hasMore := range []bool{true, false} {
			s := &missingSkillStore{hasMore: hasMore}
			h, tenant := skillQueryHandler(t, s)
			w := skillQueryRequest(h, http.MethodGet, path+"?limit=0&unknown=1")
			want := fmt.Sprintf(`{"object":"list","data":[],"first_id":null,"last_id":null,"has_more":%t}`, hasMore)
			var got, expected any
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || json.Unmarshal([]byte(want), &expected) != nil || !reflect.DeepEqual(got, expected) {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body)
			}
			if s.limit != 0 || len(s.tenants) != 1 || s.tenants[0] != tenant {
				t.Fatalf("zero page parameters: limit=%d tenants=%v", s.limit, s.tenants)
			}
		}
	}
}

// Write routes ignore unknown query keys too: the body is still validated, query
// keys never become form fields, and a foreign resource stays equal to a missing one.

func TestEnvironmentFileCreateIgnoresUnknownQueryKeys(t *testing.T) {
	h, f := environmentFileCreateHandler(t)
	request := func(id, key, body string) *httptest.ResponseRecorder {
		query := "?tenant_id=" + f.environment.TenantID + "&path=/workspace/other&unknown=1"
		r := httptest.NewRequest(http.MethodPost, "/v1/agents/environments/"+id+"/files"+query, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(environmentFilesRecorder{w}, r)
		return w
	}
	body := `{"type":"inline","data":"YWJj","path":"/workspace/a"}`
	foreign, missing := request(f.environment.ID, "other-key", body), request(uuid.NewString(), "files-key", body)
	if foreign.Code != http.StatusNotFound || foreign.Body.String() != missing.Body.String() || f.writes != 0 {
		t.Fatalf("foreign create: %d %s / %s writes=%d", foreign.Code, foreign.Body, missing.Body, f.writes)
	}
	if w := request(f.environment.ID, "files-key", `{"type":"inline","path":"/workspace/a"}`); w.Code != http.StatusBadRequest || f.writes != 0 {
		t.Fatalf("invalid body admitted: %d", w.Code)
	}
	if w := request(f.environment.ID, "files-key", body); w.Code != http.StatusCreated || f.writes != 1 || f.path != "a" || string(f.data) != "abc" {
		t.Fatalf("create: %d %s path=%q", w.Code, w.Body, f.path)
	}
}

type ownedArtifactStore struct {
	SessionArtifactStore
	owner   string
	tenants []string
	deleted int
}

func (s *ownedArtifactStore) DeleteSessionArtifact(_ context.Context, tenant, session, id string) error {
	s.tenants = append(s.tenants, tenant)
	if tenant != s.owner || session != "session" || id != "artifact" {
		return store.ErrNotFound
	}
	s.deleted++
	return nil
}

func TestArtifactDeletionIgnoresUnknownQueryKeys(t *testing.T) {
	s := &ownedArtifactStore{}
	h, owner, foreign := twoTenantHandler(t, &missingResourceStore{}, WithSessionArtifacts(s))
	s.owner = owner
	request := func(id, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodDelete, "/v1/agents/sessions/session/artifacts/"+id+"?tenant_id="+owner+"&unknown=1", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	denied, missing := request("artifact", "foreign-key"), request("missing", "test-api-key")
	if denied.Code != http.StatusNotFound || denied.Body.String() != missing.Body.String() || s.deleted != 0 || s.tenants[0] != foreign {
		t.Fatalf("foreign deletion: %d %s / %s tenants=%v", denied.Code, denied.Body, missing.Body, s.tenants)
	}
	w := request("artifact", "test-api-key")
	var body map[string]any
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["id"] != "artifact" || body["deleted"] != true || s.deleted != 1 {
		t.Fatalf("deletion: %d %s", w.Code, w.Body)
	}
}

func TestSourceFileUploadIgnoresUnknownQueryKeys(t *testing.T) {
	f := &sourceFilesFixture{}
	h, env := environmentFileCreateHandler(t, WithSourceFiles(f))
	server := newSourceFileServer(t, h)
	query := "?purpose=assistants&tenant_id=" + env.environment.TenantID + "&unknown=1"
	// A query purpose is not a form field: the body purpose is still validated.
	invalid, invalidType := sourceMultipart(t, []string{"file", "purpose=assistants"}, []byte("abc"))
	if status, _ := sourceRequest(t, server, http.MethodPost, "/v1/files?purpose=user_data", "files-key", invalidType, invalid); status != http.StatusBadRequest || f.file.ID != "" {
		t.Fatalf("query purpose admitted an upload: %d", status)
	}
	body, contentType := sourceMultipart(t, []string{"file", "purpose=user_data"}, []byte("abc"))
	status, raw := sourceRequest(t, server, http.MethodPost, "/v1/files"+query, "files-key", contentType, body)
	var file v1.SourceFile
	if status != http.StatusOK || json.Unmarshal(raw, &file) != nil || file.Purpose != "user_data" || f.tenant != env.environment.TenantID || string(f.data) != "abc" {
		t.Fatalf("upload: %d %s tenant=%s", status, raw, f.tenant)
	}
	// Another project's upload stays in its own tenant despite the tenant_id key.
	if status, _ := sourceRequest(t, server, http.MethodPost, "/v1/files"+query, "other-key", contentType, body); status != http.StatusOK || f.tenant == env.environment.TenantID {
		t.Fatalf("foreign upload changed tenant: %d %s", status, f.tenant)
	}
	if status, _ := sourceRequest(t, server, http.MethodGet, "/v1/files/"+file.ID+query, "other-key", "", nil); status != http.StatusNotFound {
		t.Fatalf("foreign read: %d", status)
	}
}

// ownedSkillStore accepts uploads and knows one owned Skill.
type ownedSkillStore struct {
	SkillStore
	owner    string
	tenants  []string
	defaults []bool
	created  int
}

func (s *ownedSkillStore) CreateSkill(_ context.Context, tenant string, _ []byte) (store.Skill, error) {
	s.tenants, s.created = append(s.tenants, tenant), s.created+1
	return store.Skill{ID: "skill_created", Name: "proof", DefaultVersion: 1, LatestVersion: 1}, nil
}

func (s *ownedSkillStore) CreateSkillVersion(_ context.Context, tenant, skill string, _ []byte, makeDefault bool) (store.SkillVersion, error) {
	s.tenants, s.defaults = append(s.tenants, tenant), append(s.defaults, makeDefault)
	if tenant != s.owner || skill != "skill_owned" {
		return store.SkillVersion{}, store.ErrNotFound
	}
	s.created++
	return store.SkillVersion{ID: "skillver_created", SkillID: skill, Name: "proof", Version: 2}, nil
}

func skillUpload(t *testing.T, include bool) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	w := multipart.NewWriter(&buffer)
	if include {
		part, err := w.CreateFormFile("files[]", "proof/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write([]byte("---\nname: proof\ndescription: Verify query tolerance.\n---\nProof.")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes(), w.FormDataContentType()
}

func TestSkillUploadsIgnoreUnknownQueryKeys(t *testing.T) {
	s := &ownedSkillStore{}
	h, owner, foreign := twoTenantHandler(t, &missingResourceStore{}, WithSkills(s))
	s.owner = owner
	server := newSourceFileServer(t, h)
	query := "?tenant_id=" + owner + "&default=true&unknown=1"
	body, contentType := skillUpload(t, true)
	empty, emptyType := skillUpload(t, false)
	if status, _ := sourceRequest(t, server, http.MethodPost, "/v1/skills"+query, "test-api-key", emptyType, empty); status != http.StatusBadRequest || s.created != 0 {
		t.Fatalf("empty upload admitted: %d", status)
	}
	status, raw := sourceRequest(t, server, http.MethodPost, "/v1/skills"+query, "test-api-key", contentType, body)
	if status != http.StatusOK || !strings.Contains(string(raw), `"id":"skill_created"`) || s.created != 1 || s.tenants[0] != owner {
		t.Fatalf("create: %d %s %v", status, raw, s.tenants)
	}
	// Version creation: a foreign Skill equals a missing one and writes nothing.
	_, denied := sourceRequest(t, server, http.MethodPost, "/v1/skills/skill_owned/versions"+query, "foreign-key", contentType, body)
	_, missing := sourceRequest(t, server, http.MethodPost, "/v1/skills/skill_missing/versions"+query, "test-api-key", contentType, body)
	if string(denied) != string(missing) || !strings.Contains(string(denied), "Resource not found.") || s.created != 1 || s.tenants[1] != foreign {
		t.Fatalf("foreign version: %s / %s tenants=%v", denied, missing, s.tenants)
	}
	status, raw = sourceRequest(t, server, http.MethodPost, "/v1/skills/skill_owned/versions"+query, "test-api-key", contentType, body)
	// The default query key is not the multipart default field.
	if status != http.StatusOK || !strings.Contains(string(raw), `"version":"2"`) || s.created != 2 || s.defaults[len(s.defaults)-1] {
		t.Fatalf("version: %d %s defaults=%v", status, raw, s.defaults)
	}
}
