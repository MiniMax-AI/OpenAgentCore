package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
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
	// The shared body gate rejects malformed bodies, including one whose first
	// key is unknown, and non-object roots (HP-09, HP-12).
	for body, message := range map[string]string{
		`{"foo":1,`:         "Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. (Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)",
		`{"type":"inline",`: "Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. (Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)",
		`{"foo":1} {}`:      "Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. (Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)",
		`[]`:                "Invalid type: expected an object, but got an array instead.",
	} {
		h, f := environmentFileCreateHandler(t)
		w := requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key")
		assertListQueryError(t, w, "invalid_request_error", nil, message)
		if f.writes != 0 {
			t.Fatal("rejected body was written", body)
		}
	}
	// Validation without an official sample keeps the local code.
	for _, body := range []string{`{"type":"inline","path":"/workspace/a"}`, `{"type":"inline","path":"/workspace/a","data":"?"}`, `{"type":"inline","path":"/workspace/a","data":"","file_id":"x"}`} {
		h, f := environmentFileCreateHandler(t)
		w := requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key")
		assertListQueryError(t, w, "invalid_request", nil, "Invalid resource identifier or request limits.")
		if f.writes != 0 {
			t.Fatal("rejected body was written", body)
		}
	}
	// Only a short, printable name is echoed; every response stays small.
	for key, echoed := range map[string]bool{
		strings.Repeat("<>", 64):   true,
		strings.Repeat("<", 257):   false,
		strings.Repeat("a", 4<<20): false,
		`tab\tkey`:                 false,
		`line\u2028separator`:      false,
		`bell\u0007`:               false,
		`caf\u00e9 \u5b57`:         true,
		`\ufffd`:                   false,
	} {
		h, f := environmentFileCreateHandler(t)
		body := `{"type":"inline","path":"/workspace/a","data":"","` + key + `":1}`
		w := requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key")
		if w.Body.Len() > 4096 || f.writes != 0 {
			t.Fatal("unknown field response is unbounded", len(key), w.Body.Len())
		}
		if echoed {
			var decoded string
			_ = json.Unmarshal([]byte(`"`+key+`"`), &decoded)
			assertListQueryError(t, w, "invalid_request_error", decoded, "Unknown parameter: '"+decoded+"'.")
		} else {
			assertListQueryError(t, w, "invalid_request_error", nil, "Unknown parameter.")
		}
	}
	// Invalid UTF-8 is rejected by the shared body gate (HP-10).
	h, f := environmentFileCreateHandler(t)
	w := requestCreateEnvironmentFile(h, f.environment.ID, "{\"type\":\"inline\",\"path\":\"/workspace/a\",\"data\":\"\",\"\xff\xfe\":1}", "files-key")
	assertListQueryError(t, w, "invalid_request_error", nil, "Invalid body: encountered a unicode decode error when parsing this JSON value. Please check the value to ensure it is valid unicode.")
	// A lone surrogate escape is a parse error (req_1a9b7680d615454ca97c816b25e2f401).
	w = requestCreateEnvironmentFile(h, f.environment.ID, `{"type":"inline","path":"/workspace/a","data":"","\ud800":1}`, "files-key")
	assertListQueryError(t, w, "invalid_request_error", nil, "Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. (Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)")
	// Foreign Environments stay missing before route-specific body validation.
	h, f = environmentFileCreateHandler(t)
	body := `{"type":"inline","path":"/workspace/a","data":"","extra_field":1}`
	foreign := requestCreateEnvironmentFile(h, f.environment.ID, body, "other-key")
	missing := requestCreateEnvironmentFile(h, uuid.NewString(), body, "files-key")
	if foreign.Code != 404 || foreign.Body.String() != missing.Body.String() || f.writes != 0 {
		t.Fatal("foreign create inspected", foreign.Code, foreign.Body)
	}
}

func TestEnvironmentFilesHostedProvisioning(t *testing.T) {
	const message = "the hosted environment is still provisioning; wait until it is connected before accessing files"
	const hosted = `{"type":"openai_hosted","network":{"access":"disabled"}}`
	const createBody = `{"type":"inline","data":"YWJj","path":"/workspace/a"}`
	sources := &sourceFilesFixture{}
	h, f := environmentFileCreateHandler(t, sources.wire)
	f.environment.Configuration, f.environment.Status = json.RawMessage(hosted), "pending"

	w := requestEnvironmentFiles(h, f.environment.ID, "?path=/workspace/a", "files-key")
	assertListQueryError(t, w, "invalid_request_error", nil, message)
	w = requestCreateEnvironmentFile(h, f.environment.ID, createBody, "files-key")
	assertListQueryError(t, w, "invalid_request_error", nil, message)
	// The check precedes the source lookup: a missing file_id is not reported.
	w = requestCreateEnvironmentFile(h, f.environment.ID, `{"type":"file_id","file_id":"file-missing","path":"/workspace/a"}`, "files-key")
	assertListQueryError(t, w, "invalid_request_error", nil, message)
	if f.reads != 0 || f.writes != 0 || sources.reads != 0 {
		t.Fatal("provisioning Environment reached execution", f.reads, f.writes, sources.reads)
	}

	// Request validation still reports its own error first.
	assertListQueryError(t, requestEnvironmentFiles(h, f.environment.ID, "?path=/workspace/a/", "files-key"),
		"invalid_request_error", nil, "path must identify a non-reserved directory inside /workspace")
	assertListQueryError(t, requestCreateEnvironmentFile(h, f.environment.ID, `{"type":"inline","data":"","path":"rel"}`, "files-key"),
		"invalid_request_error", nil, "environment.files[0].path must be an absolute POSIX path inside /workspace")

	// Foreign and missing Environments stay identical 404s.
	for _, method := range []string{"GET", "POST"} {
		var foreign, missing *httptest.ResponseRecorder
		if method == "GET" {
			foreign, missing = requestEnvironmentFiles(h, f.environment.ID, "", "other-key"), requestEnvironmentFiles(h, uuid.NewString(), "", "files-key")
		} else {
			foreign, missing = requestCreateEnvironmentFile(h, f.environment.ID, createBody, "other-key"), requestCreateEnvironmentFile(h, uuid.NewString(), createBody, "files-key")
		}
		if foreign.Code != 404 || foreign.Body.String() != missing.Body.String() {
			t.Fatal("foreign provisioning Environment disclosed", method, foreign.Code, foreign.Body)
		}
	}

	// Other states and placements keep the existing execution path.
	for _, state := range []struct{ configuration, status string }{
		{hosted, "connected"}, {hosted, "disconnected"}, {`{"type":"self_hosted","workspace_directory":"/workspace"}`, "pending"},
	} {
		f.environment.Configuration, f.environment.Status = json.RawMessage(state.configuration), state.status
		reads, writes := f.reads, f.writes
		if w := requestEnvironmentFiles(h, f.environment.ID, "", "files-key"); w.Code != 200 || f.reads != reads+1 {
			t.Fatal("list rejected", state, w.Code, w.Body)
		}
		if w := requestCreateEnvironmentFile(h, f.environment.ID, createBody, "files-key"); w.Code != 201 || f.writes != writes+1 {
			t.Fatal("create rejected", state, w.Code, w.Body)
		}
	}
}
