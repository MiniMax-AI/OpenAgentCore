package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Agent configuration protocol validation is owned by Core. It covers saved
// Agent create and update bodies and the inline Session agent, and runs before
// the configuration parsers (which keep Core's local limits and codes) and
// before harness admission. It walks the raw JSON along the pinned SDK shapes
// (PersistedAgentToolParam/AgentToolParam, AgentTextParam, AgentReasoningParam,
// MultiAgentConfigParam and the service_tier literal) and reports the first
// violation as a fieldError with the JSON path as param and the official
// message forms. Values the pinned types leave open, such as JSON Schemas,
// request metadata, MCP transport members, metadata and x_agents_core, are
// left to their existing parsers.

type valueKind uint8

const (
	anyValue     valueKind = iota // validated by the existing parsers
	stringValue                   // str
	booleanValue                  // bool
	integerValue                  // int with an inclusive minimum
	enumValue                     // Literal[...] of strings
	arrayValue                    // Iterable/SequenceNotStr of items
	mapValue                      // Dict[str, object]
	openObject                    // an object whose members are validated elsewhere
	objectValue                   // TypedDict members
	unionValue                    // TypedDicts selected by their "type" member
)

type shape struct {
	kind     valueKind
	required bool
	nullable bool
	minimum  int64
	values   []string            // enum values, or union types in the pinned order
	members  []member            // objectValue members
	variants map[string][]member // unionValue members other than "type"
	items    *shape              // arrayValue items
}

type member struct {
	name string
	shape
}

var (
	requiredString = shape{kind: stringValue, required: true}
	nullableString = shape{kind: stringValue, nullable: true}
	stringList     = shape{kind: arrayValue, nullable: true, items: &shape{kind: stringValue}}

	agentTools = shape{kind: arrayValue, nullable: true, items: &shape{kind: unionValue,
		values: []string{"function", "tool_search", "programmatic_tool_calling", "mcp", "web_search"},
		variants: map[string][]member{
			"function": {
				{"description", requiredString}, {"name", requiredString},
				{"parameters", shape{kind: mapValue, required: true}}, {"defer_loading", shape{kind: booleanValue}},
			},
			"tool_search":               nil,
			"programmatic_tool_calling": {{"enabled", shape{kind: booleanValue}}},
			"mcp": {
				{"server_label", requiredString}, {"transport", shape{kind: openObject, required: true}},
				{"allowed_tools", stringList},
				{"connection_origin", shape{kind: enumValue, nullable: true, values: []string{"service", "environment"}}},
				{"credential_id", nullableString}, {"request_metadata", shape{kind: mapValue, nullable: true}},
				{"required", shape{kind: booleanValue}},
			},
			"web_search": {
				{"allowed_domains", stringList},
				{"context_size", shape{kind: enumValue, nullable: true, values: []string{"low", "medium", "high"}}},
				{"location", shape{kind: objectValue, nullable: true, members: []member{
					{"city", nullableString}, {"country", nullableString}, {"region", nullableString}, {"timezone", nullableString},
				}}},
				{"mode", shape{kind: enumValue, nullable: true, values: []string{"disabled", "cached", "live"}}},
			},
		}}}
	agentText = shape{kind: objectValue, nullable: true, members: []member{
		{"format", shape{kind: unionValue, nullable: true, values: []string{"text", "json_schema"},
			variants: map[string][]member{"text": nil, "json_schema": {{"schema", shape{kind: mapValue, required: true}}}}}},
		{"verbosity", shape{kind: enumValue, nullable: true, values: []string{"low", "medium", "high"}}},
	}}
	agentReasoning = shape{kind: objectValue, nullable: true, members: []member{
		{"effort", shape{kind: enumValue, nullable: true, values: []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}}},
		{"summary", shape{kind: enumValue, nullable: true, values: []string{"concise", "detailed", "auto"}}},
	}}
	agentMultiAgent = shape{kind: objectValue, nullable: true, members: []member{
		{"enabled", shape{kind: booleanValue, required: true}},
		{"max_concurrent_subagents", shape{kind: integerValue, minimum: 1}},
	}}

	savedAgentCreate = agentShape(true, true)
	savedAgentUpdate = agentShape(true, false)
	sessionAgent     = agentShape(false, false)
)

// agentShape returns AgentCreateParams, AgentUpdateParams or the Session Agent.
// Saved Agents additionally carry name and metadata; only creation requires a model.
func agentShape(saved, create bool) shape {
	members := []member{
		{"model", shape{kind: stringValue, required: create}},
		{"instructions", nullableString},
		{"multi_agent", agentMultiAgent},
		{"reasoning", agentReasoning},
		{"service_tier", shape{kind: enumValue, nullable: true, values: []string{"auto", "default", "flex", "priority", "fast"}}},
		{"text", agentText},
		{"tools", agentTools},
		{"x_agents_core", shape{}},
	}
	if saved {
		members = append(members, member{"name", nullableString}, member{"metadata", shape{}})
	}
	return shape{kind: objectValue, members: members}
}

// validateSavedAgentBody checks a saved Agent create or update body. Malformed
// and non-object bodies keep the existing whole-body error.
func validateSavedAgentBody(raw []byte, root shape) error {
	if !json.Valid(raw) || jsonValueKind(raw) != "an object" {
		return nil
	}
	return validateAgentConfiguration("", raw, root)
}

// validateSessionAgent checks the inline Session agent, whose paths start with agent.
func validateSessionAgent(raw json.RawMessage) error {
	return validateAgentConfiguration("agent", raw, sessionAgent)
}

func validateAgentConfiguration(path string, raw json.RawMessage, root shape) error {
	if err := checkValue(path, raw, root); err != nil {
		return err
	}
	_, fields := orderedMembers(raw)
	var tools []json.RawMessage
	if value := fields["tools"]; jsonValueKind(value) == "an array" {
		_ = json.Unmarshal(value, &tools)
	}
	var text struct {
		Format *struct {
			Type   string          `json:"type"`
			Schema json.RawMessage `json:"schema"`
		} `json:"format"`
	}
	if value := fields["text"]; jsonValueKind(value) == "an object" {
		_ = json.Unmarshal(value, &text)
	}
	if text.Format == nil {
		return configurationConflict(tools, "", nil)
	}
	return configurationConflict(tools, text.Format.Type, text.Format.Schema)
}

func checkValue(path string, raw json.RawMessage, s shape) error {
	got := jsonValueKind(raw)
	if s.kind == anyValue || got == "null" && s.nullable {
		return nil
	}
	switch s.kind {
	case stringValue, enumValue:
		if got != "a string" {
			return invalidType(path, "a string", got)
		}
		if s.kind == enumValue {
			var value string
			if json.Unmarshal(raw, &value) != nil || !containsString(s.values, value) {
				return invalidValue(path, value, s.values)
			}
		}
	case booleanValue:
		if got != "a boolean" {
			return invalidType(path, "a boolean", got)
		}
	case integerValue:
		if got != "an integer" {
			return invalidType(path, "an integer", got)
		}
		return checkMinimum(path, string(bytes.TrimSpace(raw)), s.minimum)
	case arrayValue:
		if got != "an array" {
			return invalidType(path, "an array", got)
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
		for i, item := range items {
			if err := checkValue(fmt.Sprintf("%s[%d]", path, i), item, *s.items); err != nil {
				return err
			}
		}
	case mapValue:
		if got != "an object" {
			return invalidType(path, "an object with string keys and unknown value values", got)
		}
	case openObject:
		if got != "an object" {
			return invalidType(path, "an object", got)
		}
	case objectValue:
		if got != "an object" {
			return invalidType(path, "an object", got)
		}
		return checkMembers(path, raw, s.members)
	case unionValue:
		if got != "an object" {
			return invalidType(path, "an object", got)
		}
		_, fields := orderedMembers(raw)
		kind, supplied := fields["type"]
		if !supplied {
			return missingParameter(joinPath(path, "type"))
		}
		if got := jsonValueKind(kind); got != "a string" {
			return invalidType(joinPath(path, "type"), "a string", got)
		}
		var value string
		_ = json.Unmarshal(kind, &value)
		variant, known := s.variants[value]
		if !known {
			return invalidValue(joinPath(path, "type"), value, s.values)
		}
		return checkMembers(path, raw, append([]member{{"type", shape{}}}, variant...))
	}
	return nil
}

// checkMembers rejects unknown and repeated members in document order, then
// validates the values in document order and the required members in the pinned
// order. Names match exactly, so a name that differs from a member only by case
// is unknown. encoding/json matches names case-insensitively and merges repeated
// objects into pointer structs, so only an object whose members each appear once
// with their exact names decodes to the checked values.
func checkMembers(path string, raw json.RawMessage, members []member) error {
	keys, fields := orderedMembers(raw)
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if _, known := findMember(members, key); !known {
			if !echoableField(key) {
				return errUnknownParameter
			}
			return &fieldError{param: joinPath(path, key), message: fmt.Sprintf("Unknown parameter: '%s'.", joinPath(path, key))}
		}
		if seen[key] {
			// A local message: the official response to a repeated member is unobserved.
			return &fieldError{param: joinPath(path, key), message: fmt.Sprintf("Duplicate parameter: '%s'.", joinPath(path, key))}
		}
		seen[key] = true
	}
	for _, key := range keys {
		spec, _ := findMember(members, key)
		if err := checkValue(joinPath(path, key), fields[key], spec.shape); err != nil {
			return err
		}
	}
	for _, spec := range members {
		if _, supplied := fields[spec.name]; spec.required && !supplied {
			return missingParameter(joinPath(path, spec.name))
		}
	}
	return nil
}

// configurationConflict reports the official semantic rejections of a
// well-formed configuration: a non-object schema root, a repeated function name
// and a repeated web_search or tool_search declaration. Schemas without a string
// root type are unchanged. Session admission also applies it to the resolved
// saved configuration, so records saved before these checks cannot execute.
func configurationConflict(tools []json.RawMessage, formatType string, schema json.RawMessage) error {
	names, declared := map[string]bool{}, map[string]bool{}
	for _, raw := range tools {
		var tool struct {
			Type       string          `json:"type"`
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
		}
		if json.Unmarshal(raw, &tool) != nil {
			continue
		}
		switch tool.Type {
		case "function":
			if root, typed := schemaRootType(tool.Parameters); typed && root != "object" {
				return functionSchemaError(tool.Name, root)
			}
			if names[tool.Name] {
				if !echoableField(tool.Name) {
					return &fieldError{message: "duplicate function tool name"}
				}
				return &fieldError{message: "duplicate function tool name: " + tool.Name}
			}
			names[tool.Name] = true
		case "web_search", "tool_search":
			if declared[tool.Type] {
				return &fieldError{message: "duplicate " + tool.Type + " tool"}
			}
			declared[tool.Type] = true
		}
	}
	if root, typed := schemaRootType(schema); formatType == "json_schema" && typed && root != "object" {
		// The official message names the Session path on saved Agents too.
		if !echoableField(root) {
			return &fieldError{message: `agent.text.format.schema must have top-level type "object"`}
		}
		return &fieldError{message: fmt.Sprintf(`agent.text.format.schema must have top-level type "object"; got "%s"`, root)}
	}
	return nil
}

func functionSchemaError(name, root string) error {
	subject, got := "function", ""
	if echoableField(name) {
		subject = fmt.Sprintf("function '%s'", name)
	}
	if echoableField(root) {
		got = fmt.Sprintf(`, got 'type: "%s"'`, root)
	}
	return &fieldError{message: fmt.Sprintf(`Invalid schema for %s: schema must be a JSON Schema of 'type: "object"'%s.`, subject, got)}
}

// schemaRootType returns a schema object's top-level type when it is a string.
// A map keeps the key match exact; struct decoding would also match "Type".
func schemaRootType(schema json.RawMessage) (string, bool) {
	var root map[string]json.RawMessage
	var value string
	if json.Unmarshal(schema, &root) != nil || jsonValueKind(root["type"]) != "a string" || json.Unmarshal(root["type"], &value) != nil {
		return "", false
	}
	return value, true
}

// errUnknownParameter keeps the official code for an unknown member whose name
// is not echoed (the Environment Files rule).
var errUnknownParameter = &fieldError{message: "Unknown parameter."}

func missingParameter(path string) error {
	return &fieldError{param: path, message: fmt.Sprintf("Missing required parameter: '%s'.", path)}
}

func invalidType(path, expected, got string) error {
	return &fieldError{param: path, message: fmt.Sprintf("Invalid type for '%s': expected %s, but got %s instead.", path, expected, got)}
}

// invalidValue repeats a short printable value only, like unknown member names.
func invalidValue(path, value string, supported []string) error {
	quoted := make([]string, len(supported))
	for i, value := range supported {
		quoted[i] = "'" + value + "'"
	}
	list := strings.Join(quoted, ", ")
	if len(quoted) == 2 {
		list = quoted[0] + " and " + quoted[1]
	} else if len(quoted) > 2 {
		list = strings.Join(quoted[:len(quoted)-1], ", ") + ", and " + quoted[len(quoted)-1]
	}
	if !echoableField(value) {
		return &fieldError{param: path, message: "Invalid value. Supported values are: " + list + "."}
	}
	return &fieldError{param: path, message: fmt.Sprintf("Invalid value: '%s'. Supported values are: %s.", value, list)}
}

// checkMinimum receives an integer literal; values outside int64 are only below
// the minimum when negative, and are then not repeated.
func checkMinimum(path, literal string, minimum int64) error {
	value, err := strconv.ParseInt(literal, 10, 64)
	if err == nil && value >= minimum || err != nil && !strings.HasPrefix(literal, "-") {
		return nil
	}
	message := fmt.Sprintf("Invalid '%s': integer below minimum value. Expected a value >= %d", path, minimum)
	if err == nil {
		message += fmt.Sprintf(", but got %d instead.", value)
	} else {
		message += "."
	}
	return &fieldError{param: path, message: message}
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func findMember(members []member, name string) (member, bool) {
	for _, candidate := range members {
		if candidate.name == name {
			return candidate, true
		}
	}
	return member{}, false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// orderedMembers returns every key of an object in document order, including
// repeated keys, and each key's last value. The input is valid JSON.
func orderedMembers(raw json.RawMessage) ([]string, map[string]json.RawMessage) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	fields := map[string]json.RawMessage{}
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, fields
	}
	var keys []string
	for decoder.More() {
		token, err := decoder.Token()
		key, isKey := token.(string)
		var value json.RawMessage
		if err != nil || !isKey || decoder.Decode(&value) != nil {
			return keys, fields
		}
		keys = append(keys, key)
		fields[key] = value
	}
	return keys, fields
}
