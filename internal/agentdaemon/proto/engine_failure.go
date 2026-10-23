package proto

import "encoding/json"

// NormalizeEngineFailure accepts only neutral, finite classifications. Optional
// metadata from newer or malformed peers never changes terminal/usage decoding.
func NormalizeEngineFailure(code string, status *int) (string, *int) {
	switch code {
	case "authentication_error", "rate_limit_exceeded", "usage_limit_exceeded", "server_overloaded", "server_error", "invalid_request", "resource_not_found", "request_timeout", "context_length_exceeded", "cyber_policy":
		return code, nil
	case "connection_failed":
		if status != nil && *status >= 100 && *status <= 599 {
			return code, status
		}
		return code, nil
	default:
		return "", nil
	}
}

func (p *ErrorPayload) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Error      string          `json:"error"`
		Code       json.RawMessage `json:"code"`
		HTTPStatus json.RawMessage `json:"http_status"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	var code string
	var status *int
	_ = json.Unmarshal(wire.Code, &code)
	_ = json.Unmarshal(wire.HTTPStatus, &status)
	p.Error = wire.Error
	p.Code, p.HTTPStatus = NormalizeEngineFailure(code, status)
	return nil
}
