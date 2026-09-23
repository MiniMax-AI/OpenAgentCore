package api

import (
	"net/url"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// Official Environment Files wire rows F1–F9 of the environment-files-wire batch.

func TestEnvironmentFilesPageEnvelope(t *testing.T) {
	h, f := environmentFilesHandler(t, true)
	w := requestEnvironmentFiles(h, f.environment.ID, "", "files-key")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"object":"page","data":[],"next":null,"has_more":false}` {
		t.Fatalf("empty page: %d %s", w.Code, w.Body)
	}
	f.result.Entries = []proto.WorkspaceDirectoryEntry{environmentFileEntry("a", 1), environmentFileEntry("b", 2), environmentFileEntry("c", 3)}
	q := url.Values{"limit": {"2"}, "order": {"asc"}}
	first := decodeEnvironmentFiles(t, requestEnvironmentFiles(h, f.environment.ID, "?"+q.Encode(), "files-key"))
	if len(first.Data) != 2 || first.Next == nil || !first.HasMore || first.Object != "page" {
		t.Fatal("continued page", first)
	}
	q.Set("page", *first.Next)
	last := decodeEnvironmentFiles(t, requestEnvironmentFiles(h, f.environment.ID, "?"+q.Encode(), "files-key"))
	if len(last.Data) != 1 || last.Next != nil || last.HasMore || last.Data[0].Path != "/workspace/c" {
		t.Fatal("final page", last)
	}
}

func TestEnvironmentFilesIgnoresUnknownQueryKeys(t *testing.T) {
	h, f := environmentFilesHandler(t, true)
	f.result.Entries = []proto.WorkspaceDirectoryEntry{environmentFileEntry("a", 1)}
	want := requestEnvironmentFiles(h, f.environment.ID, "", "files-key").Body.String()
	for _, query := range []string{"?foo=bar", "?after=x&after=y", "?tenant_id=" + uuid.NewString(), "?include=all&foo", "?Path=/workspace/secret"} {
		f.directory = "unset"
		w := requestEnvironmentFiles(h, f.environment.ID, query, "files-key")
		if w.Code != 200 || w.Body.String() != want || f.directory != "" {
			t.Fatalf("%s: %d %s directory=%q", query, w.Code, w.Body, f.directory)
		}
	}
	// Unknown keys never bypass authorization: a foreign caller still sees 404.
	if w := requestEnvironmentFiles(h, f.environment.ID, "?foo=bar", "other-key"); w.Code != 404 {
		t.Fatal("foreign read with unknown key", w.Code)
	}
}

func TestEnvironmentFilesRejectsRepeatedQueryKeys(t *testing.T) {
	for _, key := range []string{"path", "limit", "order", "page"} {
		t.Run(key, func(t *testing.T) {
			h, f := environmentFilesHandler(t, true)
			query := "?" + key + "=1&" + key + "=2"
			w := requestEnvironmentFiles(h, f.environment.ID, query, "files-key")
			assertListQueryError(t, w, "invalid_request_error", nil, "Failed to deserialize query string: duplicate field `"+key+"`")
			if f.reads != 0 {
				t.Fatal("repeated key reached the reader")
			}
			foreign := requestEnvironmentFiles(h, f.environment.ID, query, "other-key")
			missing := requestEnvironmentFiles(h, uuid.NewString(), query, "files-key")
			if foreign.Code != 404 || foreign.Body.String() != missing.Body.String() {
				t.Fatal("foreign duplicate differs from missing", foreign.Code, foreign.Body, missing.Body)
			}
		})
	}
}

func TestEnvironmentFilesPathErrors(t *testing.T) {
	const directory = "path must be an absolute directory inside /workspace"
	const canonical = "path must identify a non-reserved directory inside /workspace"
	for path, message := range map[string]string{
		"/workspace/outputs/":           canonical,
		"/workspace/":                   canonical,
		"/workspace//a":                 canonical,
		"/workspace/./a":                canonical,
		"/workspace/a/..":               canonical,
		"/workspace/../workspace":       canonical,
		"/workspace/../etc":             canonical,
		"outputs":                       directory,
		"":                              directory,
		"workspace/a/":                  directory,
		"/workspace/a\x00":              directory,
		"/workspace/a\\b":               directory,
		"/workspace/a\nb":               directory,
		"/workspace/\xff":               directory,
		"/" + strings.Repeat("a", 4096): directory,
		"/workspace-sibling":            directory,
		"/etc":                          directory,
		"/":                             directory,
	} {
		t.Run(path[:min(len(path), 40)], func(t *testing.T) {
			h, f := environmentFilesHandler(t, true)
			w := requestEnvironmentFiles(h, f.environment.ID, "?"+url.Values{"path": {path}}.Encode(), "files-key")
			assertListQueryError(t, w, "invalid_request_error", nil, message)
			if f.reads != 0 {
				t.Fatal("invalid path reached the reader")
			}
		})
	}
	for path, relative := range map[string]string{"/workspace": "", "/workspace/a": "a", "/workspace/a/b c": "a/b c"} {
		h, f := environmentFilesHandler(t, true)
		decodeEnvironmentFiles(t, requestEnvironmentFiles(h, f.environment.ID, "?"+url.Values{"path": {path}}.Encode(), "files-key"))
		if f.directory != relative {
			t.Fatal("canonical path changed", path, f.directory)
		}
	}
}

func TestEnvironmentFilesPageTokenErrors(t *testing.T) {
	h, f := environmentFilesHandler(t, true)
	f.result.Entries = []proto.WorkspaceDirectoryEntry{environmentFileEntry("a", 1), environmentFileEntry("b", 2)}
	page := decodeEnvironmentFiles(t, requestEnvironmentFiles(h, f.environment.ID, "?limit=1", "files-key"))
	for name, query := range map[string]url.Values{
		"garbage":   {"page": {"garbage"}},
		"empty":     {"page": {""}},
		"oversized": {"page": {strings.Repeat("a", 1025)}},
		"binding":   {"page": {*page.Next}, "limit": {"2"}},
		"stale":     {"page": {*page.Next}, "limit": {"1"}},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "stale" {
				f.result.Entries = []proto.WorkspaceDirectoryEntry{environmentFileEntry("a", 1), environmentFileEntry("b", 3)}
			}
			w := requestEnvironmentFiles(h, f.environment.ID, "?"+query.Encode(), "files-key")
			assertListQueryError(t, w, "invalid_request_error", nil, "Invalid file page token for this request")
		})
	}
}

func TestEnvironmentFileCreateFieldErrors(t *testing.T) {
	const absolute = "environment.files[0].path must be an absolute POSIX path inside /workspace"
	const components = "environment.files[0].path cannot contain empty, . or .. path components"
	type expected struct{ param, message string }
	for body, want := range map[string]expected{
		`{"type":"inline","path":"rel.txt","data":"cg=="}`:                              {"", absolute},
		`{"type":"inline","path":"/workspace","data":"cg=="}`:                           {"", absolute},
		`{"type":"inline","path":"/workspace/slash/","data":"cg=="}`:                    {"", components},
		`{"type":"inline","path":"/workspace/../escape.txt","data":"cg=="}`:             {"", components},
		`{"type":"inline","path":"/workspace/n1/../dotdot.txt","data":"cg=="}`:          {"", components},
		`{"type":"inline","path":"/workspace/./dot.txt","data":"cg=="}`:                 {"", components},
		`{"type":"inline","path":"/workspace/nul\u0000.txt","data":"cg=="}`:             {"", absolute},
		`{"type":"inline","path":"/tmp/outside.txt","data":"cg=="}`:                     {"", absolute},
		`{"type":"inline","path":"/workspace//dbl.txt","data":"cg=="}`:                  {"", components},
		`{"type":"inline","path":"/workspacex/a","data":"cg=="}`:                        {"", absolute},
		`{"type":"file_id","path":"relative","file_id":"file-x"}`:                       {"", absolute},
		`{"type":"inline","path":"/workspace/extra.txt","data":"eA==","extra_field":1}`: {"extra_field", "Unknown parameter: 'extra_field'."},
		`{"second":1,"type":"inline","path":"/workspace/a","data":"","first":2}`:        {"second", "Unknown parameter: 'second'."},
		`{"type":"inline","path":"rel.txt","data":"?","extra_field":null}`:              {"extra_field", "Unknown parameter: 'extra_field'."},
	} {
		h, f := environmentFileCreateHandler(t)
		w := requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key")
		var param any
		if want.param != "" {
			param = want.param
		}
		assertListQueryError(t, w, "invalid_request_error", param, want.message)
		if f.writes != 0 {
			t.Fatal("rejected body was written", body)
		}
	}
	// Validation without an official sample keeps the local code.
	for _, body := range []string{`{"type":"inline","path":"/workspace/a"}`, `{"type":"inline","path":"/workspace/a","data":"?"}`, `{"type":"inline","path":"/workspace/a","data":"","file_id":"x"}`, `[]`} {
		h, f := environmentFileCreateHandler(t)
		w := requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key")
		assertListQueryError(t, w, "invalid_request", nil, "Invalid resource identifier or request limits.")
		if f.writes != 0 {
			t.Fatal("rejected body was written", body)
		}
	}
	// Foreign Environments stay missing before any body inspection.
	h, f := environmentFileCreateHandler(t)
	body := `{"type":"inline","path":"/workspace/a","data":"","extra_field":1}`
	foreign := requestCreateEnvironmentFile(h, f.environment.ID, body, "other-key")
	missing := requestCreateEnvironmentFile(h, uuid.NewString(), body, "files-key")
	if foreign.Code != 404 || foreign.Body.String() != missing.Body.String() || f.writes != 0 {
		t.Fatal("foreign create inspected", foreign.Code, foreign.Body)
	}
}
