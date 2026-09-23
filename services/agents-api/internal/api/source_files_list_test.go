package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestSourceFileListParametersAndEnvelope(t *testing.T) {
	f := &sourceFilesFixture{}
	h, _ := environmentFileCreateHandler(t, WithSourceFiles(f))
	server := newSourceFileServer(t, h)

	status, raw := sourceRequest(t, server, http.MethodGet, "/v1/files", "files-key", "", nil)
	var empty map[string]any
	if status != http.StatusOK || json.Unmarshal(raw, &empty) != nil {
		t.Fatalf("default list: %d %s", status, raw)
	}
	wantEmpty := map[string]any{"object": "list", "data": []any{}, "has_more": false, "first_id": nil, "last_id": nil}
	if !reflect.DeepEqual(empty, wantEmpty) || f.listCalls != 1 || f.listAfter != "" || f.listLimit != 10000 || f.listAsc || f.listPurpose != nil {
		t.Fatalf("default list changed: %s calls=%d after=%q limit=%d asc=%t purpose=%v", raw, f.listCalls, f.listAfter, f.listLimit, f.listAsc, f.listPurpose)
	}

	file := store.SourceFile{ID: "file-00000000-0000-0000-0000-000000000001", Filename: "one.bin", Purpose: "user_data", SizeBytes: 3, CreatedAt: time.Unix(123, 0)}
	f.listPage = store.SourceFilePage{Files: []store.SourceFile{file}, NextCursor: file.ID}
	status, raw = sourceRequest(t, server, http.MethodGet, "/v1/files?after="+file.ID+"&limit=7&order=asc&purpose=user_data", "files-key", "", nil)
	var page v1.SourceFileList
	if status != http.StatusOK || json.Unmarshal(raw, &page) != nil {
		t.Fatalf("parameterized list: %d %s", status, raw)
	}
	if !reflect.DeepEqual(page.Data, []v1.SourceFile{sourceFileResponse(file)}) || !page.HasMore || page.FirstID == nil || *page.FirstID != file.ID || page.LastID == nil || *page.LastID != file.ID {
		t.Fatalf("page projection changed: %+v", page)
	}
	if f.listAfter != file.ID || f.listLimit != 7 || !f.listAsc || f.listPurpose == nil || *f.listPurpose != "user_data" {
		t.Fatalf("parameters changed: after=%q limit=%d asc=%t purpose=%v", f.listAfter, f.listLimit, f.listAsc, f.listPurpose)
	}
}

func TestSourceFileListRejectsInvalidQueriesBeforeStorage(t *testing.T) {
	for _, query := range []string{
		"limit=", "limit=0", "limit=10001", "limit=1.5", "limit=1&limit=2",
		"order=", "order=invalid", "after=a&after=b", "purpose=a&purpose=b", "tenant_id=foreign",
	} {
		f := &sourceFilesFixture{}
		h, _ := environmentFileCreateHandler(t, WithSourceFiles(f))
		server := newSourceFileServer(t, h)
		status, _ := sourceRequest(t, server, http.MethodGet, "/v1/files?"+query, "files-key", "", nil)
		if status != http.StatusBadRequest || f.listCalls != 0 {
			t.Fatalf("invalid %q: status=%d calls=%d", query, status, f.listCalls)
		}
	}
}

func TestSourceFileListMapsStorageErrors(t *testing.T) {
	f := &sourceFilesFixture{listErr: store.ErrNotFound}
	h, _ := environmentFileCreateHandler(t, WithSourceFiles(f))
	server := newSourceFileServer(t, h)
	status, _ := sourceRequest(t, server, http.MethodGet, "/v1/files?after=file-missing", "files-key", "", nil)
	if status != http.StatusNotFound || f.listCalls != 1 {
		t.Fatalf("storage error: status=%d calls=%d", status, f.listCalls)
	}
}

func TestSourceFileListPurposeValidationBeforeCursorLookup(t *testing.T) {
	for _, purpose := range []string{"", "user_data", "assistants", "batch", "fine-tune", "vision", "evals", "assistants_output", "batch_output", "fine-tune-results", "unknown", "USER_DATA"} {
		t.Run("purpose="+purpose, func(t *testing.T) {
			f := &sourceFilesFixture{listErr: store.ErrNotFound}
			h, _ := environmentFileCreateHandler(t, WithSourceFiles(f))
			server := newSourceFileServer(t, h)
			status, raw := sourceRequest(t, server, http.MethodGet, "/v1/files?after=file-missing&purpose="+purpose, "files-key", "", nil)
			wantStatus, wantParam, wantCalls := http.StatusNotFound, "after", 1
			if purpose == "unknown" || purpose == "USER_DATA" {
				wantStatus, wantParam, wantCalls = http.StatusBadRequest, "purpose", 0
			}
			var body map[string]map[string]any
			if status != wantStatus || json.Unmarshal(raw, &body) != nil || f.listCalls != wantCalls {
				t.Fatalf("purpose validation: %d %s calls=%d", status, raw, f.listCalls)
			}
			if e := body["error"]; e["type"] != "invalid_request_error" || e["code"] != nil || e["param"] != wantParam {
				t.Fatalf("purpose error projection: %s", raw)
			}
			if wantCalls == 1 && (f.listPurpose == nil || *f.listPurpose != purpose) {
				t.Fatalf("purpose filter changed: %v", f.listPurpose)
			}
		})
	}
}

func newSourceFileServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}
