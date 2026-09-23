package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// Official body messages (HP-09..HP-15).
const (
	bodyParseMessage       = "Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. (Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)"
	bodyUnicodeMessage     = "Invalid body: encountered a unicode decode error when parsing this JSON value. Please check the value to ensure it is valid unicode."
	bodyContentTypeMessage = "expected request with Content-Type: application/json"
)

func duplicateKeyMessage(key, path string) string {
	return "Invalid body: duplicate JSON key '" + key + "' at '" + path + "'. Duplicate JSON keys are not supported."
}

func TestJSONObjectBodyChecks(t *testing.T) {
	long := strings.Repeat("k", 257)
	for body, message := range map[string]string{
		// B1 (HP-09).
		`{"name":`:               bodyParseMessage,
		`{"name":"a"}x`:          bodyParseMessage,
		`{"name":"a"}{}`:         bodyParseMessage,
		"\ufeff{\"name\":\"a\"}": bodyParseMessage,
		"  \n ":                  bodyParseMessage,
		`{"a":1,}`:               bodyParseMessage,
		`{"a":'b'}`:              bodyParseMessage,
		`nul`:                    bodyParseMessage,
		`{"a":"\q"}`:             bodyParseMessage,
		strings.Repeat("[", 10001) + strings.Repeat("]", 10001): bodyParseMessage,
		// B2 (HP-10): invalid UTF-8 anywhere, before any parse error.
		"{\"name\":\"scan6-\xff\"}": bodyUnicodeMessage,
		"{\"\xc3\x28\":1}":          bodyUnicodeMessage,
		"{\"name\":\xff":            bodyUnicodeMessage,
		"\xef\xbb":                  bodyUnicodeMessage,
		// B3 (HP-11): object keys joined by '.', array indices omitted.
		`{"name":"a","name":"b"}`:                                        duplicateKeyMessage("name", "name"),
		`{"metadata":{"k":"1","k":"2"}}`:                                 duplicateKeyMessage("k", "metadata.k"),
		`{"tools":[{"type":"function","type":"function"}]}`:              duplicateKeyMessage("type", "tools.type"),
		`{"a":[[{"b":1}],[{"c":{"d":1,"d":2}}]]}`:                        duplicateKeyMessage("d", "a.c.d"),
		`{"a":1,"\u0061":2}`:                                             duplicateKeyMessage("a", "a"),
		`{"x":{"k":1},"y":{"k":1},"x":2}`:                                duplicateKeyMessage("x", "x"),
		`{"n":1e400,"s":"}","n":1}`:                                      duplicateKeyMessage("n", "n"),
		`{"":1,"":2}`:                                                    duplicateKeyMessage("", ""),
		`{"":{"a":1,"a":2}}`:                                             duplicateKeyMessage("a", ".a"),
		`[{"a":1,"a":2}]`:                                                duplicateKeyMessage("a", "a"),
		`{"events":[{"input":[{"content":[{"text":"x","text":"y"}]}]}]}`: duplicateKeyMessage("text", "events.input.content.text"),
		// Bounded echo (echotext.Allowed) for the key and its path.
		`{"` + long + `":1,"` + long + `":2}`:                                  "Invalid body: duplicate JSON key. Duplicate JSON keys are not supported.",
		`{"` + long[:200] + `":{"` + long[:60] + `":1,"` + long[:60] + `":2}}`: "Invalid body: duplicate JSON key. Duplicate JSON keys are not supported.",
		`{"tab\tkey":1,"tab\tkey":2}`:                                          "Invalid body: duplicate JSON key. Duplicate JSON keys are not supported.",
		`{"\u2028":1,"\u2028":2}`:                                              "Invalid body: duplicate JSON key. Duplicate JSON keys are not supported.",
		// A lone or mis-paired surrogate escape is invalid JSON, in keys and values,
		// before a later duplicate (official req_1a9b7680d615454ca97c816b25e2f401).
		`{"name":"\ud800"}`:          bodyParseMessage,
		`{"name":"\udc00"}`:          bodyParseMessage,
		`{"name":"a\uD83D"}`:         bodyParseMessage,
		`{"name":"\ud83d\u0041"}`:    bodyParseMessage,
		`{"name":"\ud83d\ud83d"}`:    bodyParseMessage,
		`{"name":"\ude00\ud83d"}`:    bodyParseMessage,
		`{"name":"\ud83d\n\ude00"}`:  bodyParseMessage,
		`{"\ud800":1,"\ud800":2}`:    bodyParseMessage,
		`["\ud800"]`:                 bodyParseMessage,
		`"\ud800"`:                   bodyParseMessage,
		`{"a":1,"b":"\udfff","a":2}`: bodyParseMessage,
		`{"a":1,"a":"\udfff"}`:       duplicateKeyMessage("a", "a"),
		// Names differing only in case are distinct keys; escapes compare decoded.
		`{"k":1,"K":2,"\u004b":3}`:                 duplicateKeyMessage("K", "K"),
		`{"\ud83d\ude00":1,"😀":2}`:                 duplicateKeyMessage("😀", "😀"),
		`{"a\\":1,"a\u005c":2}`:                    duplicateKeyMessage(`a\`, `a\`),
		`{"a\/b":1,"a/b":2}`:                       duplicateKeyMessage("a/b", "a/b"),
		`{"x":{"\u0078":{"a\"b":1,"a\u0022b":2}}}`: duplicateKeyMessage(`a"b`, `x.x.a"b`),
		`{"<>":{"caf\u00e9":1,"café":2}}`:          duplicateKeyMessage("café", "<>.café"),
		// B4 (HP-12, HP-14).
		`"scan6"`:         "Invalid type: expected an object, but got a string instead.",
		`5`:               "Invalid type: expected an object, but got an integer instead.",
		` -1.5e3 `:        "Invalid type: expected an object, but got a number instead.",
		`true`:            "Invalid type: expected an object, but got a boolean instead.",
		`[]`:              "Invalid type: expected an object, but got an array instead.",
		`[{"model":"m"}]`: "Invalid type: expected an object, but got an array instead.",
	} {
		_, err := jsonObjectBody([]byte(body))
		field, ok := err.(*fieldError)
		if !ok || field.param != "" || field.message != message {
			t.Errorf("%.80q: got %v, want %q", body, err, message)
		}
	}
	// Paths are built only for the reported key, so a deep body with long keys
	// allocates linearly: building the path at every level would need gigabytes.
	depth, key := 4000, strings.Repeat("k", 1000)
	deep := strings.Repeat(`{"`+key+`":[`, depth) + `{"a":1,"a":2}` + strings.Repeat("]}", depth)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := jsonObjectBody([]byte(deep))
	runtime.ReadMemStats(&after)
	if err != errBodyDuplicateKey {
		t.Errorf("deep duplicate: %v", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4*uint64(len(deep)) {
		t.Errorf("deep duplicate allocated %d bytes for %d", allocated, len(deep))
	}
	deep = strings.Repeat(`{"b":[`, 100) + `{"a":1,"a":2}` + strings.Repeat("]}", 100)
	if _, err := jsonObjectBody([]byte(deep)); err == nil || err.Error() != duplicateKeyMessage("a", strings.Repeat("b.", 100)+"a") {
		t.Errorf("nested duplicate: %v", err)
	}
	// B5 (HP-13) and B7: null and a zero-length body are {}; objects are unchanged.
	for body, want := range map[string]string{
		``:                                      `{}`,
		`null`:                                  `{}`,
		" \tnull\r\n":                           `{}`,
		`{}`:                                    `{}`,
		` {"a":[{"b":1},{"b":2}],"c":{"b":3}} `: ` {"a":[{"b":1},{"b":2}],"c":{"b":3}} `,
		`{"x_agents_core":{"model_provider":null},"metadata":{"k":"v"}}`: `{"x_agents_core":{"model_provider":null},"metadata":{"k":"v"}}`,
		`{"name":"\u00e9\ud83d\ude00"}`:                                  `{"name":"\u00e9\ud83d\ude00"}`,
		`{"k":1,"K":2,"Metadata":{},"metadata":{}}`:                      `{"k":1,"K":2,"Metadata":{},"metadata":{}}`,
		`{"a\\":1,"a\\\\":2,"a\"":3,"a\u005cb":4}`:                       `{"a\\":1,"a\\\\":2,"a\"":3,"a\u005cb":4}`,
	} {
		got, err := jsonObjectBody([]byte(body))
		if err != nil || string(got) != want {
			t.Errorf("%q: got %q %v, want %q", body, got, err, want)
		}
	}
}

func TestJSONContentType(t *testing.T) {
	for value, want := range map[string]bool{
		"application/json":                                true,
		"application/json; charset=utf-8":                 true,
		"Application/JSON":                                true,
		" application/json ;charset=UTF-8":                true,
		"application/merge-patch+json":                    true,
		"APPLICATION/VND.API+JSON; x=y":                   true,
		"":                                                false,
		"text/plain":                                      false,
		"application/x-www-form-urlencoded":               false,
		"multipart/form-data; boundary=x":                 false,
		"text/json":                                       false,
		"application/jsonx":                               false,
		"application/json-seq":                            false,
		"application/+json":                               false,
		"application/json+xml":                            false,
		"application / json":                              false,
		"application/foo bar+json":                        false,
		"application/json garbage":                        false,
		"application/json; charset":                       false,
		"application/json; charset=utf-8; charset=latin1": false,
		"application/json;;":                              false,
		"application/json, text/plain":                    false,
		"application/json; charset=\"utf-8\"":             true,
		"application/json;":                               true,
		"application/octet-stream; t=+json":               false,
		"application/x-json-stream; t=a+json":             false,
	} {
		if got := jsonContentType(value); got != want {
			t.Errorf("%q: got %t, want %t", value, got, want)
		}
	}
}

// jsonRoute is one Agents API JSON route family. The gate precedes any lookup,
// so the fixture store is never reached by a rejected body.
type jsonRoute struct{ name, path string }

func agentsJSONRoutes() []jsonRoute {
	id := uuid.NewString()
	return []jsonRoute{
		{"agent create", "/v1/agents"},
		{"agent update", "/v1/agents/" + id},
		{"vault create", "/v1/vaults"},
		{"credential create", "/v1/vaults/" + id + "/credentials"},
		{"credential update", "/v1/vaults/" + id + "/credentials/" + id},
		{"template create", "/v1/agents/environments/templates"},
		{"template update", "/v1/agents/environments/templates/" + id},
		{"environment file create", "/v1/agents/environments/" + id + "/files"},
		{"session create", "/v1/agents/sessions"},
		{"session update", "/v1/agents/sessions/" + id},
		{"session events", "/v1/agents/sessions/" + id + "/events"},
	}
}

func bodyGateRequest(h http.Handler, path, contentType string, body []byte, headers ...string) *httptest.ResponseRecorder {
	var reader io.Reader = http.NoBody
	if body != nil {
		reader = bytes.NewReader(body)
	}
	r := httptest.NewRequest(http.MethodPost, path, reader)
	r.Header.Set("Authorization", "Bearer test-api-key")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func officialBodyError(message string) string {
	encoded, _ := json.Marshal(message)
	return `{"error":{"message":` + string(encoded) + `,"type":"invalid_request_error","code":"invalid_request_error","param":null}}` + "\n"
}

// Every Agents API JSON route rejects B1-B4 and B6 before any decoding, lookup
// or write, with the official fields (HP-09..HP-15).
func TestAgentsJSONRoutesShareBodyGate(t *testing.T) {
	cases := []struct {
		name, contentType string
		body              []byte
		message           string
	}{
		{"malformed", "application/json", []byte(`{"name":`), bodyParseMessage},
		{"trailing garbage", "application/json", []byte(`{"name":"a"}x`), bodyParseMessage},
		{"two objects", "application/json", []byte(`{"name":"a"}{}`), bodyParseMessage},
		{"bom", "application/json", []byte("\ufeff{}"), bodyParseMessage},
		{"whitespace", "application/json", []byte("  \n "), bodyParseMessage},
		{"invalid utf-8", "application/json", []byte("{\"name\":\"scan6-\xff\"}"), bodyUnicodeMessage},
		{"duplicate", "application/json", []byte(`{"name":"a","name":"b"}`), duplicateKeyMessage("name", "name")},
		{"nested duplicate", "application/json", []byte(`{"metadata":{"k":"1","k":"2"}}`), duplicateKeyMessage("k", "metadata.k")},
		{"string root", "application/json", []byte(`"scan6"`), "Invalid type: expected an object, but got a string instead."},
		{"array root", "application/json", []byte(`[]`), "Invalid type: expected an object, but got an array instead."},
		{"no content type", "", []byte(`{"name":"a"}`), bodyContentTypeMessage},
		{"text/plain", "text/plain", []byte(`{"name":"a"}`), bodyContentTypeMessage},
		{"form", "application/x-www-form-urlencoded", []byte(`{"name":"a"}`), bodyContentTypeMessage},
		{"bodyless without content type", "", nil, bodyContentTypeMessage},
		// B6 precedes the body limit.
		{"large text/plain", "text/plain", bytes.Repeat([]byte(" "), 17<<20), bodyContentTypeMessage},
	}
	for _, route := range agentsJSONRoutes() {
		h, s := validationHandler(t)
		for _, tc := range cases {
			w := bodyGateRequest(h, route.path, tc.contentType, tc.body, "Idempotency-Key", "gate-key")
			if w.Code != http.StatusBadRequest || w.Body.String() != officialBodyError(tc.message) {
				t.Errorf("%s %s: %d %s", route.name, tc.name, w.Code, w.Body)
			}
		}
		if s.writes != 0 {
			t.Fatalf("%s: rejected body reached storage", route.name)
		}
	}
}

// Authentication and Beta handling precede the gate; the body limit follows
// the Content-Type check and precedes parsing.
func TestAgentsJSONBodyGateOrder(t *testing.T) {
	h, s := validationHandler(t)
	for _, route := range agentsJSONRoutes() {
		// A bad Content-Type and a malformed body prove the order.
		r := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(`{"name":`))
		r.Header.Set("Authorization", "Bearer test-api-key")
		r.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"invalid_beta"`) {
			t.Errorf("%s: missing Beta: %d %s", route.name, w.Code, w.Body)
		}
		r = httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(`{"name":`))
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Content-Type", "text/plain")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: missing auth: %d %s", route.name, w.Code, w.Body)
		}
		// An oversized malformed body reports its route limit.
		size := 16<<20 + 1
		if strings.HasSuffix(route.path, "/files") {
			size = ((proto.WorkspaceWriteMaxBytes+2)/3)*4 + (16 << 10) + 1
		}
		w = bodyGateRequest(h, route.path, "application/json", append([]byte(`{"name":`), bytes.Repeat([]byte("x"), size)...))
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: oversized: %d %s", route.name, w.Code, w.Body)
		}
	}
	if s.writes != 0 {
		t.Fatal("rejected body reached storage")
	}
}

// B5 and B7: a zero-length body or null is {}, accepted JSON media types keep
// valid bodies unchanged, and route validation still follows the gate.
func TestAgentsJSONBodyGateKeepsValidBodies(t *testing.T) {
	h, s := validationHandler(t)
	param := func(value string) *string { return &value }
	for _, body := range []string{``, `null`} {
		assertConfigurationError(t, bodyGateRequest(h, "/v1/agents", "application/json", []byte(body)), "invalid_request_error", param("model"), "Missing required parameter: 'model'.")
		assertConfigurationError(t, bodyGateRequest(h, "/v1/agents/sessions/"+uuid.NewString(), "application/json", []byte(body)), "invalid_request_error", nil, "At least one update field is required")
		if w := bodyGateRequest(h, "/v1/agents/"+uuid.NewString(), "application/json", []byte(body)); w.Code != http.StatusOK {
			t.Fatalf("empty Agent update %q: %d %s", body, w.Code, w.Body)
		}
		if w := bodyGateRequest(h, "/v1/vaults", "application/json", []byte(body)); w.Code != http.StatusCreated {
			t.Fatalf("empty Vault create %q: %d %s", body, w.Code, w.Body)
		}
	}
	if s.writes != 4 {
		t.Fatalf("writes = %d", s.writes)
	}
	for _, contentType := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON", "application/merge-patch+json"} {
		if w := bodyGateRequest(h, "/v1/agents", contentType, []byte(`{"model":"m","name":"a","x_agents_core":{"model_provider":null}}`)); w.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", contentType, w.Code, w.Body)
		}
	}
	assertConfigurationError(t, bodyGateRequest(h, "/v1/agents", "application/json", []byte(`{"model":"m","tool_choice":"auto"}`)), "invalid_request_error", param("tool_choice"), "Unknown parameter: 'tool_choice'.")
	assertConfigurationError(t, bodyGateRequest(h, "/v1/agents/"+uuid.NewString(), "application/json", []byte(`{"model":4}`)), "invalid_request_error", param("model"), "Invalid type for 'model': expected a string, but got an integer instead.")
	if w := bodyGateRequest(h, "/v1/agents", "application/json", []byte(`{"model":"`+strings.Repeat("x", 1<<20)+`"}`)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("limit: %d %s", w.Code, w.Body)
	}
}

// referenceDuplicateJSONKey walks encoding/json tokens; the byte scanner must
// agree with it.
func referenceDuplicateJSONKey(raw []byte) (string, string, bool) {
	type container struct {
		keys   map[string]bool
		member string
		inside bool
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var stack []*container
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", "", false
		}
		if delim, ok := token.(json.Delim); ok && (delim == '}' || delim == ']') {
			stack = stack[:len(stack)-1]
			if len(stack) > 0 {
				stack[len(stack)-1].inside = false
			}
			continue
		}
		var top *container
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if top != nil && top.keys != nil && !top.inside {
			key := token.(string)
			if top.keys[key] {
				var segments []string
				for _, c := range stack[:len(stack)-1] {
					if c.keys != nil {
						segments = append(segments, c.member)
					}
				}
				return key, strings.Join(append(segments, key), "."), true
			}
			top.keys[key], top.member, top.inside = true, key, true
			continue
		}
		if delim, ok := token.(json.Delim); ok {
			next := &container{}
			if delim == '{' {
				next.keys = map[string]bool{}
			}
			stack = append(stack, next)
		} else if top != nil {
			top.inside = false
		}
	}
}

// The scan agrees with the reference on small objects and on objects with more
// than smallObjectKeys members, which use the open-addressing set.
func TestDuplicateJSONKeyMatchesReference(t *testing.T) {
	names := []string{`a`, `b`, `\u0061`, `a\"b`, `a\\b`, `a\u005cb`, ``, `k,:{}[]`, `\ud83d\ude00`, `😀`, `\ufffd`, `é`, `\u00e9`, `K`, `k`}
	values := []string{`1`, `-2.5e3`, `true`, `null`, `"x,y:{}[]"`, `"\"a\":1"`, `"\\"`, `"\ud83d\ude00"`, `[]`, `{}`}
	random := uint64(1)
	next := func(n int) int {
		random = random*6364136223846793005 + 1442695040888963407
		return int(random>>33) % n
	}
	large := 0
	var value func(depth, width, suffixes int) string
	value = func(depth, width, suffixes int) string {
		switch choice := next(4); {
		case depth > 3 || choice == 0:
			return values[next(len(values))]
		case choice == 1:
			items := make([]string, next(4))
			for i := range items {
				items[i] = value(depth+1, width, suffixes)
			}
			return "[" + strings.Join(items, ",") + "]"
		default:
			members := make([]string, next(width))
			if len(members) > smallObjectKeys {
				large++
			}
			// Members of a large object nest less deeply, in small objects.
			child, childWidth := depth+1, width
			if width > smallObjectKeys {
				child, childWidth = depth+2, 6
			}
			for i := range members {
				members[i] = `"` + names[next(len(names))] + fmt.Sprint(next(suffixes)) + `": ` + value(child, childWidth, suffixes)
			}
			return "{" + strings.Join(members, ",") + "}"
		}
	}
	duplicates := 0
	for i := range 20000 {
		width, suffixes := 6, 3
		if i%2 == 1 {
			width, suffixes = 60, 200
		}
		body := []byte(value(0, width, suffixes))
		if !json.Valid(body) {
			t.Fatalf("invalid generated body %s", body)
		}
		key, path, found, err := scanJSON(body)
		wantKey, wantPath, wantFound := referenceDuplicateJSONKey(body)
		if err != nil || key != wantKey || path != wantPath || found != wantFound {
			t.Fatalf("%s: got %q %q %t %v, want %q %q %t", body, key, path, found, err, wantKey, wantPath, wantFound)
		}
		if found {
			duplicates++
		}
	}
	if duplicates < 1000 || duplicates > 19000 || large < 1000 {
		t.Fatalf("unbalanced generated bodies: %d duplicates, %d large objects", duplicates, large)
	}
	// Deterministic large objects: the repeat at every position, escaped forms
	// and nesting inside a large object.
	for size := smallObjectKeys + 1; size <= 300; size += 7 {
		for _, repeat := range []int{0, smallObjectKeys - 1, smallObjectKeys, size / 2, size - 1} {
			members := make([]string, size)
			for i := range members {
				members[i] = fmt.Sprintf(`"k%d":{"k%d":[{"x":1}]}`, i, i)
			}
			valid := []byte("{" + strings.Join(members, ",") + "}")
			if _, _, found, err := scanJSON(valid); found || err != nil {
				t.Fatalf("size %d: false duplicate %v", size, err)
			}
			escaped := fmt.Sprintf(`"\u006b%d":2`, repeat)
			body := []byte("{" + strings.Join(append(members, escaped), ",") + "}")
			key, path, found, err := scanJSON(body)
			if want := fmt.Sprintf("k%d", repeat); !found || err != nil || key != want || path != want {
				t.Fatalf("size %d repeat %d: %q %q %t %v", size, repeat, key, path, found, err)
			}
		}
	}
}

// A body of many short keys needs memory proportional to its key count, not
// a copy of every key: 116 MiB was allocated for this 16 MiB body before, and
// about 32 MiB in total, the growing set of 8-byte slots, is allocated now.
func TestDuplicateJSONKeyMemory(t *testing.T) {
	body := manyShortKeys(16 << 20)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _, found, err := scanJSON(body)
	runtime.ReadMemStats(&after)
	if found || err != nil {
		t.Fatal(found, err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 5*uint64(len(body))/2 {
		t.Fatalf("allocated %d MiB for a %d MiB body", allocated>>20, len(body)>>20)
	}
}

func BenchmarkDuplicateJSONKeyManyShortKeys(b *testing.B) {
	body := manyShortKeys(16 << 20)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		if _, _, found, err := scanJSON(body); found || err != nil {
			b.Fatal(found, err)
		}
	}
}

func manyShortKeys(size int) []byte {
	var body bytes.Buffer
	body.WriteString("{")
	for i := 0; body.Len() < size; i++ {
		fmt.Fprintf(&body, `"k%07d":0,`, i)
	}
	body.WriteString(`"z":0}`)
	return body.Bytes()
}
