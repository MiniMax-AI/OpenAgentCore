package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
)

// caseVariantMember reports whether raw has an object key, at any depth that
// decodes into a struct of type t, that encoding/json would match to a field
// only case-insensitively. The official service matches member names exactly,
// so such a key is an unknown member, never an alias of the field
// (req_6ba2a50c71a4410f87a1baac855e82df). Unknown keys and values of other types
// are left to the decoder. raw has passed the shared body gate.
func caseVariantMember(raw []byte, t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == rawMessageType || reflect.PointerTo(t).Implements(unmarshalerType) {
		return false
	}
	switch t.Kind() {
	case reflect.Struct:
		var members map[string]json.RawMessage
		if json.Unmarshal(raw, &members) != nil {
			return false
		}
		fields := jsonFields(t)
		for key, value := range members {
			field, exact := fields[key]
			if exact {
				if caseVariantMember(value, field) {
					return true
				}
				continue
			}
			for name := range fields {
				if strings.EqualFold(name, key) {
					return true
				}
			}
		}
	case reflect.Map:
		var members map[string]json.RawMessage
		if json.Unmarshal(raw, &members) != nil {
			return false
		}
		for _, value := range members {
			if caseVariantMember(value, t.Elem()) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return false
		}
		for _, item := range items {
			if caseVariantMember(item, t.Elem()) {
				return true
			}
		}
	}
	return false
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
