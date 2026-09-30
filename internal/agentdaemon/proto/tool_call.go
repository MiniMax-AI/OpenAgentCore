package proto

import (
	"bytes"
	"encoding/json"
)

func (p *ToolCallPayload) UnmarshalJSON(raw []byte) error {
	type toolCall ToolCallPayload
	var value toolCall
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*p = ToolCallPayload(value)
	return nil
}
