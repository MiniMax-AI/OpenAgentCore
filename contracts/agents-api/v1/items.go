package v1

import (
	"bytes"
	"encoding/json"
)

// UnmarshalJSON preserves integer precision in tool arguments and structured results.
func (i *Item) UnmarshalJSON(raw []byte) error {
	type wire Item
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if value.Type == "function_call_output" {
		var fields struct {
			Output json.RawMessage
			Error  json.RawMessage
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		if value.Output == nil && len(fields.Output) > 0 {
			value.Output = fields.Output
		}
		if value.Error == nil && len(fields.Error) > 0 {
			value.Error = fields.Error
		}
	}
	// MCP has required nullable output/error fields.
	if value.Type == "mcp_call" {
		if value.Output == nil {
			value.Output = json.RawMessage(`null`)
		}
		if value.Error == nil {
			value.Error = json.RawMessage(`null`)
		}
	}
	if value.Arguments == nil && (value.Type == "mcp_call" || value.Type == "function_call") {
		value.Arguments = json.RawMessage(`null`)
	}
	*i = Item(value)
	return nil
}
