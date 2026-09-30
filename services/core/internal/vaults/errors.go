package vaults

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/echotext"
)

var (
	// ErrNotFound reports a Vault or Credential that does not exist in the
	// caller's tenant, including one another tenant owns and an ID that cannot
	// name one.
	ErrNotFound = errors.New("vault resource not found")
	// ErrInvalidInput rejects a request the operation cannot accept.
	ErrInvalidInput = errors.New("invalid vault request")
)

// MCPCredentialSelectionError rejects a Session MCP credential selection with
// the observed official message (MV-03); the API reports a Conflict as 409
// conflict_error and any other as 400 invalid_request_error. Selection searches
// only the attached Vaults, which the caller owns, so a missing, foreign-tenant,
// unattached or malformed reference produces the same error, and only a
// credential of an attached Vault can report a server_url mismatch.
type MCPCredentialSelectionError struct {
	Conflict bool
	Message  string
}

func (e *MCPCredentialSelectionError) Error() string { return e.Message }

// echoed repeats a caller-supplied value in a selection message only within
// the shared bound; otherwise the message leaves it out.
func echoed(value string) string {
	if !echotext.Allowed(value) {
		return ""
	}
	return " " + value
}

func mcpCredentialRequiresVault() error {
	return &MCPCredentialSelectionError{Message: "MCP credential_id requires an attached vault"}
}

func mcpCredentialNotAttached(id string) error {
	return &MCPCredentialSelectionError{Message: "MCP credential_id" + echoed(id) + " was not found in an attached vault"}
}

func mcpCredentialURLMismatch(id, url string) error {
	return &MCPCredentialSelectionError{Message: "MCP credential_id" + echoed(id) + " does not match server_url" + echoed(url)}
}

func mcpCredentialAmbiguous(url string) error {
	return &MCPCredentialSelectionError{Conflict: true, Message: "multiple attached vault credentials match MCP server_url" + echoed(url) + "; specify credential_id"}
}
