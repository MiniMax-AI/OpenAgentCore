package harnessconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// ErrHarnessConfig deliberately excludes caller-controlled keys and values.
var ErrHarnessConfig = errors.New("invalid or unsupported harness_config")

const MaxHarnessConfigBytes = 16 * 1024

func decodeHarnessConfig(raw json.RawMessage) (proto.HarnessConfig, error) {
	if len(raw) == 0 {
		return proto.HarnessConfig{}, nil
	}
	if len(raw) > MaxHarnessConfigBytes || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return nil, ErrHarnessConfig
	}
	if !uniqueMembers(json.NewDecoder(bytes.NewReader(raw))) {
		return nil, ErrHarnessConfig
	}
	var result proto.HarnessConfig
	if json.Unmarshal(raw, &result) != nil || result == nil {
		return nil, ErrHarnessConfig
	}
	return result, nil
}

func (c Configuration) ParseHarnessConfig(raw json.RawMessage) (proto.HarnessConfig, error) {
	result, err := decodeHarnessConfig(raw)
	if err != nil {
		return nil, err
	}
	if len(result) != 0 && (c.ValidateNativeConfig == nil || !c.ValidateNativeConfig(result)) {
		return nil, ErrHarnessConfig
	}
	return result, nil
}

// PrepareHarnessConfig copies the wire object before passing it to native code.
// An absent option means {}, while an explicitly supplied null is invalid.
func (c Configuration) PrepareHarnessConfig(options map[string]any) (proto.HarnessConfig, error) {
	value, present := options["harness_config"]
	if !present {
		return proto.HarnessConfig{}, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, ErrHarnessConfig
	}
	return c.ParseHarnessConfig(raw)
}

// ValidateHarnessConfig validates a selected adapter. An empty kind is for saved
// Agents without a selected Harness: at least one registered adapter must accept
// the object. Session admission always validates its resolved kind again.
func (r Registry) ValidateHarnessConfig(kind string, raw json.RawMessage) error {
	config, err := decodeHarnessConfig(raw)
	if err != nil {
		return err
	}
	// Empty configuration needs no adapter-specific parameter qualification.
	// Provider protocol support still requires an explicit declaration.
	if len(config) == 0 {
		return nil
	}
	if kind != "" {
		c, ok := r.Lookup(kind)
		if !ok {
			return ErrHarnessConfig
		}
		_, err := c.ParseHarnessConfig(raw)
		return err
	}
	for _, c := range r.configurations {
		if _, err := c.ParseHarnessConfig(raw); err == nil {
			return nil
		}
	}
	return ErrHarnessConfig
}

// Reject repeated members before storage so values discarded by encoding/json
// cannot survive in the original RawMessage returned through a safe read API.
func uniqueMembers(decoder *json.Decoder) bool {
	var read func(int) bool
	read = func(depth int) bool {
		if depth > 32 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !read(depth + 1) {
					return false
				}
			}
		case '[':
			for decoder.More() {
				if !read(depth + 1) {
					return false
				}
			}
		default:
			return false
		}
		_, err = decoder.Token()
		return err == nil
	}
	if !read(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}
