package api

import (
	"bytes"
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type mcpCredentialResolver interface {
	ResolveMCPCredentials(context.Context, string, []string, []store.MCPCredentialRequest) ([]store.MCPCredentialBinding, error)
}

func (h *Handler) bindSessionCredentials(ctx context.Context, tenant string, raw json.RawMessage) (json.RawMessage, error) {
	var cfg configuration
	if json.Unmarshal(raw, &cfg) != nil {
		return nil, store.ErrInvalidInput
	}
	var requests []store.MCPCredentialRequest
	required := len(cfg.VaultIDs) > 0
	for _, rawTool := range cfg.Agent.Tools {
		var tool v1.MCPTool
		if json.Unmarshal(rawTool, &tool) != nil {
			return nil, store.ErrInvalidInput
		}
		if tool.Type == "mcp" {
			requests = append(requests, store.MCPCredentialRequest{ServerLabel: tool.ServerLabel, ServerURL: tool.Transport.ServerURL, CredentialID: tool.CredentialID})
			required = required || tool.CredentialID != nil
		}
	}
	if !required {
		return raw, nil
	}
	resolver, ok := h.store.(mcpCredentialResolver)
	if !ok {
		return nil, store.ErrCredentialStorageUnavailable
	}
	bindings, err := resolver.ResolveMCPCredentials(ctx, tenant, cfg.VaultIDs, requests)
	if err != nil {
		return nil, err
	}
	cfg.MCPCredentials = bindings
	return json.Marshal(cfg)
}

// projectedMCPCredential shows the credential that creation selected for an MCP
// tool without an explicit credential_id, as the official Session projection
// does (MV-02). A frozen binding names only a credential of a Vault the caller
// attached and owned at creation, and the ID stays shown after that credential
// is deleted. Anonymous selections stay null and explicit references are echoed
// unchanged. Only the response changes: the stored caller intent, creation
// retries and dispatch keep reading the configuration as stored.
func projectedMCPCredential(raw json.RawMessage, cfg configuration) json.RawMessage {
	var tool v1.MCPTool
	if len(cfg.MCPCredentials) == 0 || json.Unmarshal(raw, &tool) != nil || tool.Type != "mcp" || tool.CredentialID != nil {
		return raw
	}
	for _, binding := range cfg.MCPCredentials {
		if binding.ServerLabel != tool.ServerLabel || binding.ServerURL != tool.Transport.ServerURL || binding.CredentialID == "" {
			continue
		}
		if !attachedVault(cfg.VaultIDs, binding.VaultID) {
			return raw
		}
		// Keep the stored member order; only the credential_id value changes.
		keys, fields := orderedMembers(raw)
		if len(keys) == 0 || len(keys) != len(fields) {
			return raw
		}
		if _, present := fields["credential_id"]; !present {
			keys = append(keys, "credential_id")
		}
		fields["credential_id"], _ = json.Marshal(binding.CredentialID)
		var projected bytes.Buffer
		projected.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				projected.WriteByte(',')
			}
			name, _ := json.Marshal(key)
			projected.Write(name)
			projected.WriteByte(':')
			projected.Write(fields[key])
		}
		projected.WriteByte('}')
		return projected.Bytes()
	}
	return raw
}

func attachedVault(attached []string, vault string) bool {
	selected, err := uuid.Parse(vault)
	if err != nil {
		return false
	}
	for _, raw := range attached {
		if id, err := uuid.Parse(raw); err == nil && id == selected {
			return true
		}
	}
	return false
}

// Attached inline requests need recorded caller intent before reading mutable
// Vault contents. Other inline requests retain their resolved/default identity.
func inlineCredentialIntent(input sessionRequest) bool {
	if len(input.VaultIDs) > 0 {
		return true
	}
	var tools []struct {
		Type         string  `json:"type"`
		CredentialID *string `json:"credential_id"`
	}
	if json.Unmarshal(input.agentFields["tools"], &tools) == nil {
		for _, tool := range tools {
			if tool.Type == "mcp" && tool.CredentialID != nil {
				return true
			}
		}
	}
	return false
}
