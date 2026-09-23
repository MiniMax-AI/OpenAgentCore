package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/echotext"
)

// readJSONBody reads a bounded raw body. Agents API JSON routes use
// readJSONObject instead; DELETE and internal routes keep this reader.
func readJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	return readJSONBodyLimit(w, r, 1024*1024, "Request exceeds 1 MiB.")
}

func readJSONBodyLimit(w http.ResponseWriter, r *http.Request, limit int64, message string) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err == nil {
		return raw, true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", message)
	} else {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must contain one JSON object.")
	}
	return nil, false
}

// Official request body errors (HP-09..HP-15), reported with a null param.
var (
	errBodyContentType = &fieldError{message: "expected request with Content-Type: application/json"}
	errBodyUnicode     = &fieldError{message: "Invalid body: encountered a unicode decode error when parsing this JSON value. Please check the value to ensure it is valid unicode."}
	errBodyParse       = &fieldError{message: "Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. (Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)"}
	// errBodyDuplicateKey omits a key or path that cannot be echoed; see echotext.Allowed.
	errBodyDuplicateKey = &fieldError{message: "Invalid body: duplicate JSON key. Duplicate JSON keys are not supported."}
)

// readJSONObject is the shared body gate of every Agents API JSON route.
func readJSONObject(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	return readJSONObjectLimit(w, r, 1024*1024, "Request exceeds 1 MiB.")
}

// readJSONObjectLimit runs before any route-specific decoding, validation or
// lookup. It requires a JSON Content-Type before reading, applies the route's
// body limit, then checks the whole body with the official parse semantics:
// valid UTF-8, exactly one JSON value, no repeated object key at any depth and
// an object root. A zero-length body or null becomes {}. Routes then decode
// the returned object with their own rules.
func readJSONObjectLimit(w http.ResponseWriter, r *http.Request, limit int64, message string) ([]byte, bool) {
	if !jsonContentType(r.Header.Get("Content-Type")) {
		writeFieldError(w, errBodyContentType)
		return nil, false
	}
	raw, ok := readJSONBodyLimit(w, r, limit, message)
	if !ok {
		return nil, false
	}
	object, err := jsonObjectBody(raw)
	if err != nil {
		writeFieldError(w, err)
		return nil, false
	}
	return object, true
}

// jsonContentType accepts application/json and application/*+json media types
// case-insensitively, with any parameters.
func jsonContentType(value string) bool {
	mediaType, _, _ := strings.Cut(value, ";")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "application/json" {
		return true
	}
	subtype, ok := strings.CutPrefix(mediaType, "application/")
	return ok && len(subtype) > len("+json") && strings.HasSuffix(subtype, "+json")
}

func jsonObjectBody(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return []byte(`{}`), nil
	}
	if !utf8.Valid(raw) {
		return nil, errBodyUnicode
	}
	// A byte order mark, a whitespace-only body or trailing data is invalid JSON.
	if !json.Valid(raw) {
		return nil, errBodyParse
	}
	if key, path, found := duplicateJSONKey(raw); found {
		if !echotext.Allowed(key) || !echotext.Allowed(path) {
			return nil, errBodyDuplicateKey
		}
		return nil, &fieldError{message: fmt.Sprintf("Invalid body: duplicate JSON key '%s' at '%s'. Duplicate JSON keys are not supported.", key, path)}
	}
	switch kind := jsonValueKind(raw); kind {
	case "an object":
		return raw, nil
	case "null":
		return []byte(`{}`), nil
	default:
		// An array root is rejected too; the official service treats [] as {} (HP-14).
		return nil, &fieldError{message: fmt.Sprintf("Invalid type: expected an object, but got %s instead.", kind)}
	}
}

// duplicateJSONKey returns the first repeated object key of a valid JSON value
// in document order, with its path of object keys joined by '.'. Array indices
// are omitted, as observed officially: 'metadata.k' and 'tools.type'. Keys are
// compared after unescaping. The scan is linear, and the path is built only for
// the reported key.
func duplicateJSONKey(raw []byte) (key, path string, found bool) {
	type frame struct {
		object    bool
		expectKey bool
		keys      [][]byte            // the keys of a small object
		set       map[string]struct{} // every key once an object is large
		member    []byte              // the key whose value is being read
	}
	var stack []frame
	for i := 0; i < len(raw); {
		switch raw[i] {
		case '{', '[':
			if len(stack) < cap(stack) {
				// Reuse the key slice of an earlier sibling at this depth.
				stack = stack[:len(stack)+1]
				keys := stack[len(stack)-1].keys[:0]
				stack[len(stack)-1] = frame{keys: keys}
			} else {
				stack = append(stack, frame{})
			}
			top := &stack[len(stack)-1]
			top.object, top.expectKey = raw[i] == '{', raw[i] == '{'
			i++
		case '}', ']':
			stack = stack[:len(stack)-1]
			i++
		case ',':
			if top := &stack[len(stack)-1]; top.object {
				top.expectKey = true
			}
			i++
		case '"':
			end := i + 1
			for {
				end += bytes.IndexAny(raw[end:], "\\\"")
				if raw[end] == '"' {
					break
				}
				end += 2
			}
			end++
			if len(stack) == 0 || !stack[len(stack)-1].expectKey {
				i = end
				continue
			}
			top := &stack[len(stack)-1]
			name := raw[i+1 : end-1]
			if bytes.IndexByte(name, '\\') >= 0 {
				var decoded string
				_ = json.Unmarshal(raw[i:end], &decoded)
				name = []byte(decoded)
			}
			repeated := false
			if top.set != nil {
				_, repeated = top.set[string(name)]
				top.set[string(name)] = struct{}{}
			} else {
				repeated = slices.ContainsFunc(top.keys, func(k []byte) bool { return bytes.Equal(k, name) })
				top.keys = append(top.keys, name)
				if len(top.keys) > 16 {
					top.set = make(map[string]struct{}, 32)
					for _, k := range top.keys {
						top.set[string(k)] = struct{}{}
					}
				}
			}
			if repeated {
				var segments []string
				for _, f := range stack[:len(stack)-1] {
					if f.object {
						segments = append(segments, string(f.member))
					}
				}
				return string(name), strings.Join(append(segments, string(name)), "."), true
			}
			top.member, top.expectKey = name, false
			i = end
		default:
			i++
		}
	}
	return "", "", false
}
