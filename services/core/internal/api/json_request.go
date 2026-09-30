package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/maphash"
	"mime"
	"net/http"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/echotext"
)

// readJSONBody reads a bounded raw body. Agents API JSON routes use
// readJSONObject instead; DELETE and internal routes keep this reader.
func readJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	return readJSONBodyLimit(w, r, 1024*1024, "Request exceeds 1 MiB.")
}

func readJSONBodyLimit(w http.ResponseWriter, r *http.Request, limit int64, message string) ([]byte, bool) {
	// A doubling buffer allocates two to four times the body in total, four near
	// the limit; io.ReadAll's smaller growth steps allocate 4.4 to 6.1 times.
	var body bytes.Buffer
	_, err := body.ReadFrom(http.MaxBytesReader(w, r.Body, limit))
	if err == nil {
		return body.Bytes(), true
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
// case-insensitively, with well-formed parameters. A malformed media type is
// rejected like a missing one.
func jsonContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
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
	// Key positions are 31-bit; route limits keep bodies far below that.
	if uint64(len(raw)) >= keyEscaped || !json.Valid(raw) {
		return nil, errBodyParse
	}
	key, path, found, err := scanJSON(raw)
	if err != nil {
		return nil, err
	}
	if found {
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

// keyEscaped marks a key position whose string contains escapes. A position is
// the offset of the key's opening quote plus one, so zero means empty.
const keyEscaped = 1 << 31

// smallObjectKeys is the number of keys an object compares linearly before it
// switches to an open-addressing set.
const smallObjectKeys = 16

type scannedKey struct {
	position uint32
	hash     uint64
}

type scanFrame struct {
	object, expectKey bool
	member            uint32   // the position of the key whose value is being read
	base, count       int      // this object's keys in keyScanner.small while it is small
	table             []uint64 // hash tag << 32 | key position, once the object is large
}

// keyScanner finds repeated keys without copying them: keys are positions in
// the body, compared and hashed after unescaping only when they contain escapes.
type keyScanner struct {
	raw         []byte
	seed        maphash.Seed
	frames      []scanFrame
	small       []scannedKey
	left, right []byte // unescaping buffers
}

// scanJSON checks a valid JSON value in one linear pass in document order. A
// string escape that forms a lone or mis-paired UTF-16 surrogate is a parse
// error, as observed officially (req_1a9b7680d615454ca97c816b25e2f401); valid
// pairs are accepted. It returns the first repeated object key, compared after
// unescaping, with its path of object keys joined by '.'. Array indices are
// omitted, as observed officially: 'metadata.k' and 'tools.type'. Names differing
// only in case are distinct keys.
func scanJSON(raw []byte) (key, path string, found bool, err error) {
	s := keyScanner{raw: raw, seed: maphash.MakeSeed()}
	for i := 0; i < len(raw); {
		switch c := raw[i]; c {
		case '{', '[':
			s.frames = append(s.frames, scanFrame{object: c == '{', expectKey: c == '{', base: len(s.small)})
			i++
		case '}', ']':
			closed := &s.frames[len(s.frames)-1]
			s.small = s.small[:closed.base]
			*closed = scanFrame{}
			s.frames = s.frames[:len(s.frames)-1]
			i++
		case ',':
			if top := &s.frames[len(s.frames)-1]; top.object {
				top.expectKey = true
			}
			i++
		case '"':
			end, escaped, ok := scanString(raw, i)
			if !ok {
				return "", "", false, errBodyParse
			}
			if n := len(s.frames); n > 0 && s.frames[n-1].expectKey {
				position := uint32(i + 1)
				if escaped {
					position |= keyEscaped
				}
				if s.insert(&s.frames[n-1], position) {
					key, path := s.duplicate(position)
					return key, path, true, nil
				}
			}
			i = end
		default:
			i++
		}
	}
	return "", "", false, nil
}

// scanString returns the offset after the string starting at raw[quote] and
// whether it contains escapes, or false for an unpaired surrogate escape.
func scanString(raw []byte, quote int) (end int, escaped, ok bool) {
	for i := quote + 1; ; {
		switch raw[i] {
		case '"':
			return i + 1, escaped, true
		case '\\':
			escaped = true
			if raw[i+1] != 'u' {
				i += 2
				continue
			}
			r := hex4(raw[i+2 : i+6])
			i += 6
			if utf16.IsSurrogate(r) {
				if r >= 0xdc00 || raw[i] != '\\' || raw[i+1] != 'u' {
					return 0, true, false
				}
				if low := hex4(raw[i+2 : i+6]); low < 0xdc00 || low > 0xdfff {
					return 0, true, false
				}
				i += 6
			}
		default:
			i++
		}
	}
}

func hex4(digits []byte) rune {
	var r rune
	for _, c := range digits {
		switch {
		case c >= 'a':
			c -= 'a' - 10
		case c >= 'A':
			c -= 'A' - 10
		default:
			c -= '0'
		}
		r = r<<4 | rune(c)
	}
	return r
}

// content returns the raw bytes of the key at position, without quotes.
func (s *keyScanner) content(position uint32) []byte {
	start := int(position&^keyEscaped) - 1
	for i := start + 1; ; i++ {
		switch s.raw[i] {
		case '"':
			return s.raw[start+1 : i]
		case '\\':
			i++
		}
	}
}

func (s *keyScanner) hash(position uint32) uint64 {
	if position&keyEscaped == 0 {
		return maphash.Bytes(s.seed, s.content(position))
	}
	s.left = appendUnescaped(s.left[:0], s.content(position))
	return maphash.Bytes(s.seed, s.left)
}

// equal compares two keys after unescaping. Unescaped keys compare in place:
// their contents contain no quote, so a length mismatch fails at a quote.
func (s *keyScanner) equal(a, b uint32) bool {
	if a&keyEscaped == 0 && b&keyEscaped == 0 {
		content := s.content(b)
		start := int(a) - 1
		end := start + 1 + len(content)
		return end < len(s.raw) && s.raw[end] == '"' && bytes.Equal(s.raw[start+1:end], content)
	}
	s.left = appendUnescaped(s.left[:0], s.content(a))
	s.right = appendUnescaped(s.right[:0], s.content(b))
	return bytes.Equal(s.left, s.right)
}

// insert records a key of the top object and reports whether it repeats one.
func (s *keyScanner) insert(f *scanFrame, position uint32) bool {
	f.member, f.expectKey = position, false
	hash := s.hash(position)
	if f.table == nil {
		for _, key := range s.small[f.base : f.base+f.count] {
			if key.hash == hash && s.equal(key.position, position) {
				return true
			}
		}
		s.small = append(s.small, scannedKey{position, hash})
		if f.count++; f.count > smallObjectKeys {
			f.table = make([]uint64, 4*smallObjectKeys)
			for _, key := range s.small[f.base:] {
				place(f.table, key.position, key.hash)
			}
			s.small = s.small[:f.base]
		}
		return false
	}
	// Linear probing at a load factor of at most 3/4. The high 32 hash bits
	// stored with each position skip comparing keys that cannot be equal.
	if (f.count+1)*4 > len(f.table)*3 {
		grown := make([]uint64, 2*len(f.table))
		for _, slot := range f.table {
			if key := uint32(slot); key != 0 {
				place(grown, key, s.hash(key))
			}
		}
		f.table = grown
	}
	mask, tag := uint64(len(f.table)-1), hash>>32
	for i := hash & mask; ; i = (i + 1) & mask {
		switch slot := f.table[i]; {
		case slot == 0:
			f.table[i] = tag<<32 | uint64(position)
			f.count++
			return false
		case slot>>32 == tag && s.equal(uint32(slot), position):
			return true
		}
	}
}

func place(table []uint64, position uint32, hash uint64) {
	mask := uint64(len(table) - 1)
	i := hash & mask
	for table[i] != 0 {
		i = (i + 1) & mask
	}
	table[i] = hash>>32<<32 | uint64(position)
}

// duplicate returns the repeated key and its path through the enclosing objects.
func (s *keyScanner) duplicate(position uint32) (string, string) {
	var segments []string
	for _, f := range s.frames[:len(s.frames)-1] {
		if f.object {
			segments = append(segments, s.text(f.member))
		}
	}
	key := s.text(position)
	return key, strings.Join(append(segments, key), ".")
}

// text returns the unescaped key at position.
func (s *keyScanner) text(position uint32) string {
	if position&keyEscaped == 0 {
		return string(s.content(position))
	}
	s.left = appendUnescaped(s.left[:0], s.content(position))
	return string(s.left)
}

// appendUnescaped decodes the contents of a JSON string. Like encoding/json,
// it decodes an unpaired or mis-paired surrogate escape as U+FFFD, so such a
// key matches no member name; the body gate rejects these escapes earlier. A
// truncated escape, which valid JSON cannot contain, is kept as it is.
func appendUnescaped(dst, s []byte) []byte {
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			dst = append(dst, s[i])
			continue
		}
		if i+1 == len(s) || s[i+1] == 'u' && i+6 > len(s) {
			return append(dst, s[i:]...)
		}
		i++
		switch s[i] {
		case 'b':
			dst = append(dst, '\b')
		case 'f':
			dst = append(dst, '\f')
		case 'n':
			dst = append(dst, '\n')
		case 'r':
			dst = append(dst, '\r')
		case 't':
			dst = append(dst, '\t')
		case 'u':
			r := hex4(s[i+1 : i+5])
			i += 4
			if utf16.IsSurrogate(r) {
				low := rune(-1)
				if r < 0xdc00 && i+7 <= len(s) && s[i+1] == '\\' && s[i+2] == 'u' {
					low = hex4(s[i+3 : i+7])
				}
				if r = utf16.DecodeRune(r, low); r != utf8.RuneError {
					i += 6
				}
			}
			dst = utf8.AppendRune(dst, r)
		default:
			dst = append(dst, s[i])
		}
	}
	return dst
}
