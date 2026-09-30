package api

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
)

// inexactMember reports whether raw, a valid JSON value, has an object key that
// is not exactly a member name of the struct it decodes into, at any depth. It
// runs before a decoder with DisallowUnknownFields, which rejects unknown keys
// too but matches names case-insensitively and lets the last copy win: the
// official service treats a case variant such as Metadata as an unknown member
// (req_6ba2a50c71a4410f87a1baac855e82df). It walks the bytes once without
// copying them and stops at the first such key, so a body of unknown keys costs
// no allocation before the route's error.
func inexactMember(raw []byte, t reflect.Type) bool {
	w := memberWalker{raw: raw}
	_, found := w.value(skipSpace(raw, 0), t)
	return found
}

type memberWalker struct {
	raw     []byte
	scratch []byte // an unescaped member name
}

// value walks the value at raw[i] as type t and returns the offset after it.
func (w *memberWalker) value(i int, t reflect.Type) (int, bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == rawMessageType || reflect.PointerTo(t).Implements(unmarshalerType) {
		return skipValue(w.raw, i), false
	}
	switch {
	case w.raw[i] == '{' && (t.Kind() == reflect.Struct || t.Kind() == reflect.Map):
		var fields map[string]reflect.Type
		if t.Kind() == reflect.Struct {
			fields = jsonFields(t)
		}
		i = skipSpace(w.raw, i+1)
		for w.raw[i] != '}' {
			end := stringEnd(w.raw, i)
			name := w.raw[i+1 : end-1]
			i = skipSpace(w.raw, skipSpace(w.raw, end)+1)
			child := t
			if fields == nil {
				child = t.Elem()
			} else {
				if bytes.IndexByte(name, '\\') >= 0 {
					w.scratch = appendUnescaped(w.scratch[:0], name)
					name = w.scratch
				}
				var exact bool
				if child, exact = fields[string(name)]; !exact {
					return 0, true
				}
			}
			var found bool
			if i, found = w.value(i, child); found {
				return 0, true
			}
			i = skipComma(w.raw, skipSpace(w.raw, i))
		}
		return i + 1, false
	case w.raw[i] == '[' && (t.Kind() == reflect.Array || t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8):
		i = skipSpace(w.raw, i+1)
		for w.raw[i] != ']' {
			var found bool
			if i, found = w.value(i, t.Elem()); found {
				return 0, true
			}
			i = skipComma(w.raw, skipSpace(w.raw, i))
		}
		return i + 1, false
	}
	return skipValue(w.raw, i), false
}

func skipSpace(raw []byte, i int) int {
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == '\n' || raw[i] == '\r') {
		i++
	}
	return i
}

// skipComma steps over a separating comma and the space after it.
func skipComma(raw []byte, i int) int {
	if raw[i] == ',' {
		return skipSpace(raw, i+1)
	}
	return i
}

// stringEnd returns the offset after the string starting at raw[quote].
func stringEnd(raw []byte, quote int) int {
	for i := quote + 1; ; {
		q := i + bytes.IndexByte(raw[i:], '"')
		backslashes := 0
		for q-backslashes > i && raw[q-backslashes-1] == '\\' {
			backslashes++
		}
		if backslashes%2 == 0 {
			return q + 1
		}
		i = q + 1
	}
}

// skipValue returns the offset after the value starting at raw[i].
func skipValue(raw []byte, i int) int {
	depth := 0
	for {
		switch raw[i] {
		case '"':
			i = stringEnd(raw, i)
			if depth == 0 {
				return i
			}
			continue
		case '{', '[':
			depth++
		case '}', ']':
			if depth--; depth == 0 {
				return i + 1
			}
		default:
			if depth == 0 {
				// A number or literal ends at a delimiter or the end of the body.
				for i < len(raw) && !strings.ContainsRune(",]} \t\r\n", rune(raw[i])) {
					i++
				}
				return i
			}
		}
		i++
	}
}

var (
	rawMessageType  = reflect.TypeFor[json.RawMessage]()
	unmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	jsonFieldCache  sync.Map // reflect.Type -> map[string]reflect.Type
)

// jsonFields returns the JSON member names of a struct type with their field
// types, including promoted fields of untagged embedded structs, where the
// shallowest field of a name wins as in encoding/json.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	if cached, ok := jsonFieldCache.Load(t); ok {
		return cached.(map[string]reflect.Type)
	}
	fields := map[string]reflect.Type{}
	depths := map[string]int{}
	var collect func(t reflect.Type, depth int)
	collect = func(t reflect.Type, depth int) {
		for i := range t.NumField() {
			field := t.Field(i)
			tag := field.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if field.Anonymous && name == "" && embedded.Kind() == reflect.Struct {
				collect(embedded, depth+1)
				continue
			}
			if !field.IsExported() {
				continue
			}
			if name == "" {
				name = field.Name
			}
			if seen, ok := depths[name]; !ok || depth < seen {
				fields[name], depths[name] = field.Type, depth
			}
		}
	}
	collect(t, 0)
	jsonFieldCache.Store(t, fields)
	return fields
}

// objectMember returns the value of the exact member name of a valid JSON
// object without decoding the other members, or nil.
func objectMember(raw []byte, name string) []byte {
	i := skipSpace(raw, 0)
	if i == len(raw) || raw[i] != '{' {
		return nil
	}
	var scratch []byte
	for i = skipSpace(raw, i+1); raw[i] != '}'; {
		end := stringEnd(raw, i)
		key := raw[i+1 : end-1]
		if bytes.IndexByte(key, '\\') >= 0 {
			scratch = appendUnescaped(scratch[:0], key)
			key = scratch
		}
		i = skipSpace(raw, skipSpace(raw, end)+1)
		valueEnd := skipValue(raw, i)
		if string(key) == name {
			return raw[i:valueEnd]
		}
		i = skipComma(raw, skipSpace(raw, valueEnd))
	}
	return nil
}
