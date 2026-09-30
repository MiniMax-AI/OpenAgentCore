package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
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
			f := &sourceFilesFixture{listErr: fmt.Errorf("wrapped: %w", files.ErrNotFound)}
			h, _ := environmentFileCreateHandler(t, f.wire)
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

func TestFilesErrorOptionalParameterPreservesOtherErrors(t *testing.T) {
	for _, tc := range []struct {
		path   string
		err    error
		status int
		code   any
	}{
		{"/v1/files/file-missing", files.ErrInvalidInput, 400, "invalid_request"},
		{"/v1/files/file-missing", fmt.Errorf("database unavailable"), 500, "internal_error"},
		{"/v1/files/file-missing", files.ErrTooLarge, 413, "request_too_large"},
		{"/core/v1/projects/project/files/file-missing", files.ErrNotFound, 404, "not_found_error"},
	} {
		w := httptest.NewRecorder()
		writeFilesError(w, httptest.NewRequest(http.MethodGet, tc.path, nil), tc.err, "id")
		var body map[string]map[string]any
		if w.Code != tc.status || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("unexpected error: %d %s", w.Code, w.Body.String())
		}
		wantParam := any(nil)
		if tc.status == http.StatusNotFound {
			wantParam = "id"
		}
		if body["error"]["code"] != tc.code || body["error"]["param"] != wantParam {
			t.Fatalf("unrelated error changed: %s", w.Body.String())
		}
	}
}
