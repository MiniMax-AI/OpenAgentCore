package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestSourceFileMissingErrorParameters(t *testing.T) {
	for _, tc := range []struct{ method, path, param string }{
		{http.MethodGet, "/v1/files/file-missing", "id"},
		{http.MethodDelete, "/v1/files/file-missing", "id"},
		{http.MethodGet, "/v1/files/file-missing/content", "id"},
		{http.MethodGet, "/v1/files?after=file-missing", "after"},
		// Unknown query keys do not change single-resource or list lookups.
		{http.MethodGet, "/v1/files/file-missing?unknown=1&tenant_id=foreign", "id"},
		{http.MethodDelete, "/v1/files/file-missing?unknown=1", "id"},
		{http.MethodGet, "/v1/files/file-missing/content?unknown=1", "id"},
		{http.MethodGet, "/v1/files?after=file-missing&unknown=1", "after"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			f := &sourceFilesFixture{listErr: fmt.Errorf("wrapped: %w", store.ErrNotFound)}
			h, _ := environmentFileCreateHandler(t, WithSourceFiles(f))
			server := newSourceFileServer(t, h)
			status, raw := sourceRequest(t, server, tc.method, tc.path, "files-key", "", nil)
			var body map[string]map[string]any
			if status != http.StatusNotFound || json.Unmarshal(raw, &body) != nil {
				t.Fatalf("missing: %d %s", status, raw)
			}
			e := body["error"]
			if e["type"] != "invalid_request_error" || e["code"] != nil || e["param"] != tc.param || e["message"] != "Resource not found." {
				t.Fatalf("error projection: %s", raw)
			}
		})
	}
}

func TestStoreErrorOptionalParameterPreservesOtherErrors(t *testing.T) {
	for _, tc := range []struct {
		path   string
		err    error
		status int
		code   any
		param  []string
	}{
		{"/v1/skills/skill_missing", store.ErrNotFound, 404, nil, nil},
		{"/v1/agents/agent_missing", store.ErrNotFound, 404, "not_found_error", nil},
		{"/v1/files/file-missing", store.ErrInvalidInput, 400, "invalid_request", []string{"id"}},
		{"/v1/files/file-missing", fmt.Errorf("database unavailable"), 500, "internal_error", []string{"id"}},
	} {
		w := httptest.NewRecorder()
		writeStoreError(w, httptest.NewRequest(http.MethodGet, tc.path, nil), tc.err, tc.param...)
		var body map[string]map[string]any
		if w.Code != tc.status || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("unexpected error: %d %s", w.Code, w.Body.String())
		}
		if body["error"]["code"] != tc.code || body["error"]["param"] != nil {
			t.Fatalf("unrelated error changed: %s", w.Body.String())
		}
	}
}
