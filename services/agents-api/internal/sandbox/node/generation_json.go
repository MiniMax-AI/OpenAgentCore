package node

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

// Decode control objects before trusting Go's struct decoder: that decoder
// accepts repeated and case-insensitive keys, and silently maps null to zero.
// Each list is the actual wire surface, not a second provider payload schema.
func generationObject(raw []byte, required, optional, nullable string) (map[string]json.RawMessage, error) {
	allowed := map[string]bool{}
	nulls := map[string]bool{}
	for _, key := range strings.Fields(required + " " + optional) {
		allowed[key] = true
	}
	for _, key := range strings.Fields(nullable) {
		nulls[key] = true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, sandbox.ErrInvalid
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] || fields[key] != nil {
			return nil, sandbox.ErrInvalid
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(value, []byte("null")) && !nulls[key] {
			return nil, sandbox.ErrInvalid
		}
		fields[key] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, sandbox.ErrInvalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, sandbox.ErrInvalid
	}
	for _, key := range strings.Fields(required) {
		if fields[key] == nil {
			return nil, sandbox.ErrInvalid
		}
	}
	return fields, nil
}

func generationRecords(raw []byte, required, optional string) error {
	if raw == nil {
		return nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || entries == nil || len(entries) > 8 {
		return sandbox.ErrInvalid
	}
	for _, entry := range entries {
		if _, err := generationObject(entry, required, optional, ""); err != nil {
			return err
		}
	}
	return nil
}

func validateGenerationJSON(raw []byte, kind string) error {
	var fields, optional string
	switch kind {
	case "hello":
		fields = "identity health"
		optional = "generation_management"
	case "welcome", "heartbeat_ack":
		fields = "connection_id owner_epoch"
		optional = "deployment"
	case "heartbeat":
		fields = "health connection_id owner_epoch"
	case "retention":
		fields = "control"
	case "retention_ack":
		fields = "control deployment"
	case "request":
		fields = "request"
	case "response":
		fields = "response"
	default:
		return sandbox.ErrInvalid
	}
	values, err := generationObject(raw, "version type "+fields, optional, "")
	if err != nil {
		return err
	}
	if value := values["deployment"]; value != nil {
		if _, err := generationObject(value, "generation specification_digest serving_generation", "", "serving_generation"); err != nil {
			return err
		}
	}
	if value := values["identity"]; value != nil {
		if _, err := generationObject(value, "specification_digest deployment_generation node_id installation_id provider backend_fingerprint max_active max_retained", "", ""); err != nil {
			return err
		}
	}
	if value := values["health"]; value != nil {
		health, err := generationObject(value, "provider_ready observed_at active_operations cpu_utilization total_memory_bytes effective_cpu_cores", "generations diagnostic cpu_count available_memory_bytes available_disk_bytes", "cpu_utilization total_memory_bytes effective_cpu_cores cpu_count available_memory_bytes available_disk_bytes")
		if err != nil {
			return err
		}
		if err := generationRecords(health["generations"], "generation specification_digest state", "diagnostic"); err != nil {
			return err
		}
	}
	if value := values["control"]; value != nil {
		records := "references"
		required := "generation specification_digest"
		if kind == "retention_ack" {
			records = "retentions"
			required += " keep"
		}
		control, err := generationObject(value, "id sequence connection_id owner_epoch "+records, "", "")
		if err != nil {
			return err
		}
		if err := generationRecords(control[records], required, ""); err != nil {
			return err
		}
	}
	if value := values["request"]; value != nil {
		if _, err := generationObject(value, "deployment_generation id sequence connection_id owner_epoch operation timeout_ms reference", "bootstrap compute generation command suspend resume snapshot observation", ""); err != nil {
			return err
		}
	}
	if value := values["response"]; value != nil {
		if _, err := generationObject(value, "id connection_id", "error_code info compute state command sample", ""); err != nil {
			return err
		}
	}
	return nil
}
