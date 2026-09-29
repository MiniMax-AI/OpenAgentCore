package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// errorProbeFiles reports every File as missing and lists nothing.
type errorProbeFiles struct{ SourceFileStore }

func (errorProbeFiles) GetSourceFile(context.Context, string, string) (store.SourceFile, error) {
	return store.SourceFile{}, store.ErrNotFound
}
func (errorProbeFiles) ListSourceFiles(context.Context, string, string, int, bool, *string) (store.SourceFilePage, error) {
	return store.SourceFilePage{}, nil
}

// errorProbeSkills reports every Skill as missing, rejects every Skill version
// cursor and refuses every version deletion as the default version.
type errorProbeSkills struct{ SkillStore }

func (errorProbeSkills) GetSkill(context.Context, string, string) (store.Skill, error) {
	return store.Skill{}, store.ErrNotFound
}
func (errorProbeSkills) ListSkills(context.Context, string, string, int, bool) (store.SkillPage, error) {
	return store.SkillPage{}, nil
}
func (errorProbeSkills) ListSkillVersions(_ context.Context, _, _, after string, _ int, _ bool) (store.SkillVersionPage, error) {
	if after != "" {
		return store.SkillVersionPage{}, &store.InvalidCursorError{Message: "cursor"}
	}
	return store.SkillVersionPage{}, nil
}
func (errorProbeSkills) DeleteSkillVersion(context.Context, string, string, string) (store.SkillVersion, error) {
	return store.SkillVersion{}, store.ErrDefaultSkillVersion
}

// The Files and Skills error family is chosen by request path. Through the
// real routes, Core gets the Agents API Beta fields from the shared list
// parser and not-found mapping, while errors a handler writes itself keep their
// public fields. admin-api.md documents both; these cases keep it true.
func TestCoreFilesAndSkillsUseTheBetaErrorFamily(t *testing.T) {
	h, _, _ := adminTestHandler(t, WithSourceFiles(errorProbeFiles{}), WithSkills(errorProbeSkills{}))
	type result struct {
		status      int
		code, param any
	}
	request := func(t *testing.T, method, path, key string) result {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusOK {
			return result{status: w.Code}
		}
		var body struct {
			Error struct {
				Code  any `json:"code"`
				Param any `json:"param"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return result{w.Code, body.Error.Code, body.Error.Param}
	}
	beta := result{400, "invalid_request_error", nil}
	for _, test := range []struct {
		name, method, path string
		public, core       result
	}{
		{"missing File", http.MethodGet, "/files/file-1", result{404, nil, "id"}, result{404, "not_found_error", "id"}},
		{"missing Skill", http.MethodGet, "/skills/skill-1", result{404, nil, nil}, result{404, "not_found_error", nil}},
		{"unresolved Skill version cursor", http.MethodGet, "/skills/skill-1/versions?after=skillver_x", result{400, "invalid_value", "after"}, beta},
		{"repeated Files limit", http.MethodGet, "/files?limit=1&limit=2", result{400, "unsupported_parameter", nil}, beta},
		{"repeated Skills limit", http.MethodGet, "/skills?limit=1&limit=2", result{400, "duplicate_parameter", "limit"}, beta},
		{"invalid Files order", http.MethodGet, "/files?order=sideways", result{400, nil, nil}, beta},
		{"invalid Skills order", http.MethodGet, "/skills?order=sideways", result{400, "invalid_value", "order"}, beta},
		{"Files limit 0", http.MethodGet, "/files?limit=0", result{400, nil, nil}, beta},
		{"Skills limit 0", http.MethodGet, "/skills?limit=0", result{status: 200}, beta},
		{"Skills limit above maximum", http.MethodGet, "/skills?limit=101", result{400, "integer_above_max_value", "limit"}, beta},
		{"unknown Files purpose", http.MethodGet, "/files?purpose=bogus", result{400, nil, "purpose"}, result{400, nil, "purpose"}},
		{"default Skill version deletion", http.MethodDelete, "/skills/skill-1/versions/1", result{400, "invalid_value", "version"}, result{400, "invalid_value", "version"}},
	} {
		if got := request(t, test.method, "/v1"+test.path, "caller"); got != test.public {
			t.Errorf("%s on /v1: got %+v, want %+v", test.name, got, test.public)
		}
		if got := request(t, test.method, "/core/v1/projects/"+managementProjectID+test.path, "admin"); got != test.core {
			t.Errorf("%s on /core/v1: got %+v, want %+v", test.name, got, test.core)
		}
	}
}
