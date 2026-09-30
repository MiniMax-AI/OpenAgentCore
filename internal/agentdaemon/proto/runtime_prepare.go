package proto

import (
	"encoding/hex"
	"encoding/json"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/google/uuid"
)

const (
	TypeRuntimePrepare          = "runtime_prepare"
	TypeRuntimePrepareResult    = "runtime_prepare_result"
	RuntimePrepareMaxBytes      = 50 << 20
	RuntimePrepareChunkBytes    = WorkspaceWriteChunkBytes
	RuntimePrepareMaxFrameBytes = 1 << 20
)

// RuntimeInitialFile addresses a file within the logical workspace.
type RuntimeInitialFile struct {
	Path string `json:"path"`
}

type RuntimeInitialization struct {
	Action   string            `json:"action"`
	Env      map[string]string `json:"env,omitempty"`
	Network  string            `json:"network,omitempty"`
	Packages []string          `json:"packages,omitempty"`
	Command  string            `json:"command,omitempty"`
	CWD      string            `json:"cwd,omitempty"`
}

// RuntimePreparePayload carries typed initialization or capability preparation.
// Runtime resolves logical paths and owns installation destinations. Envelope.ID
// identifies one connection-local transfer.
type RuntimePreparePayload struct {
	Step           string                   `json:"step"`
	EnvironmentID  string                   `json:"environment_id,omitempty"`
	SessionID      string                   `json:"session_id,omitempty"`
	Action         string                   `json:"action,omitempty"`
	Slot           int                      `json:"slot,omitempty"`
	Skill          *agentskill.Metadata     `json:"skill,omitempty"`
	Plugin         *agentplugin.Metadata    `json:"plugin,omitempty"`
	File           *RuntimeInitialFile      `json:"file,omitempty"`
	Initialization *RuntimeInitialization   `json:"initialization,omitempty"`
	Sources        *agentcapabilities.Input `json:"sources,omitempty"`
	SizeBytes      int                      `json:"size_bytes,omitempty"`
	SHA256         string                   `json:"sha256,omitempty"`
	Offset         int                      `json:"offset,omitempty"`
	Data           []byte                   `json:"data,omitempty"`
}

type RuntimePrepareResultPayload struct {
	ExitCode  int    `json:"exit_code,omitempty"`
	Outcome   string `json:"outcome"`
	Offset    int    `json:"offset,omitempty"`
	SizeBytes int    `json:"size_bytes,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

func ValidRuntimePrepareRequest(p RuntimePreparePayload) bool {
	if p.Step == "begin" {
		for _, id := range []string{p.EnvironmentID, p.SessionID} {
			value, err := uuid.Parse(id)
			if err != nil || value == uuid.Nil || value.String() != id {
				return false
			}
		}
		if p.Offset != 0 || len(p.Data) != 0 {
			return false
		}
		metadata := 0
		for _, present := range []bool{p.Skill != nil, p.Plugin != nil, p.Sources != nil, p.File != nil, p.Initialization != nil} {
			if present {
				metadata++
			}
		}
		if metadata != 1 {
			return false
		}
		switch p.Action {
		case "skill":
			if p.Skill == nil || p.Plugin != nil || p.Sources != nil || p.Slot != 0 ||
				agentcapabilities.ValidateInput(agentcapabilities.Input{Skills: []agentskill.Metadata{*p.Skill}}) != nil {
				return false
			}
		case "plugin":
			if p.Plugin == nil || p.Skill != nil || p.Sources != nil || p.Slot < 0 || p.Slot >= 50 ||
				agentcapabilities.ValidateInput(agentcapabilities.Input{Plugins: []agentplugin.Metadata{*p.Plugin}}) != nil {
				return false
			}
		case "file":
			if p.File == nil || p.Slot != 0 || !validWorkspacePath(p.File.Path, false) {
				return false
			}
		case "initialize":
			if p.Initialization == nil || p.Slot != 0 || p.SizeBytes != 0 || p.SHA256 != "" || !validRuntimeInitialization(*p.Initialization) {
				return false
			}
		case "finalize":
			if p.Skill != nil || p.Plugin != nil || p.Sources == nil || p.Slot != 0 ||
				p.SizeBytes != 0 || p.SHA256 != "" || agentcapabilities.ValidateInput(*p.Sources) != nil {
				return false
			}
		default:
			return false
		}
		if p.Action != "finalize" && p.Action != "initialize" {
			digest, err := hex.DecodeString(p.SHA256)
			if err != nil || len(digest) != 32 || strings.ToLower(p.SHA256) != p.SHA256 ||
				p.SizeBytes < 0 || (p.SizeBytes == 0 && p.Action != "file") || p.SizeBytes > RuntimePrepareMaxBytes {
				return false
			}
		}
		encoded, err := json.Marshal(p)
		return err == nil && len(encoded) <= RuntimePrepareMaxFrameBytes
	}
	if p.EnvironmentID != "" || p.SessionID != "" || p.Action != "" || p.Slot != 0 ||
		p.Skill != nil || p.Plugin != nil || p.Sources != nil || p.File != nil || p.Initialization != nil || p.SizeBytes != 0 || p.SHA256 != "" {
		return false
	}
	switch p.Step {
	case "chunk":
		return p.Offset >= 0 && p.Offset <= RuntimePrepareMaxBytes && len(p.Data) > 0 &&
			len(p.Data) <= RuntimePrepareChunkBytes && len(p.Data) <= RuntimePrepareMaxBytes-p.Offset
	case "commit":
		return p.Offset == 0 && len(p.Data) == 0
	default:
		return false
	}
}

// ValidRuntimePrepareResult accepts only the expected success receipt or a finite
// terminal category. An unknown effect can never be represented as rejection.
func ValidRuntimePrepareResult(r RuntimePrepareResultPayload, expected string, offset, size int) bool {
	if r.ExitCode < 0 || r.ExitCode > 255 || (r.Outcome != "failed" && r.ExitCode != 0) {
		return false
	}
	switch r.Outcome {
	case "rejected":
		if r.Offset != 0 || r.SizeBytes != 0 {
			return false
		}
		switch r.ErrorCode {
		case "invalid_request", "resource_unavailable", "runtime_preparation_capacity", "runtime_preparation_unsupported", "runtime_preparation_rejected":
			return true
		default:
			return false
		}
	case "failed":
		return r.Offset == 0 && r.SizeBytes == 0 && r.ErrorCode == "runtime_preparation_failed"
	case "unknown":
		return r.Offset == 0 && r.SizeBytes == 0 && r.ErrorCode == "runtime_preparation_unconfirmed"
	}
	if r.Outcome != expected || r.Offset != offset || r.ErrorCode != "" {
		return false
	}
	switch expected {
	case "completed":
		return r.SizeBytes == size
	case "ready", "received":
		return r.SizeBytes == 0
	default:
		return false
	}
}

// Workspace paths are logical protocol addresses, resolved by the Runtime.
func validWorkspacePath(value string, allowRoot bool) bool {
	if len(value) > 4096 || !utf8.ValidString(value) || path.Clean(value) != value || strings.ContainsAny(value, "\\\x00\r\n") {
		return false
	}
	return (allowRoot && value == "/workspace") || strings.HasPrefix(value, "/workspace/")
}

func validRuntimeInitialization(p RuntimeInitialization) bool {
	if p.Network != "" && p.Network != "enabled" && p.Network != "disabled" {
		return false
	}
	if p.CWD != "" && !validWorkspacePath(p.CWD, true) {
		return false
	}
	switch p.Action {
	case "configure":
		if p.Packages != nil || p.Command != "" || p.CWD != "" || len(p.Env) > 1000 {
			return false
		}
		for key, value := range p.Env {
			if key == "" || !utf8.ValidString(key) || !utf8.ValidString(value) || strings.ContainsAny(key, "=\x00\r\n") || strings.ContainsRune(value, 0) {
				return false
			}
		}
		return true
	case "npm", "python":
		if p.Env != nil || p.Command != "" || p.CWD != "" || len(p.Packages) == 0 || len(p.Packages) > 1000 || p.Network == "" {
			return false
		}
		for _, value := range p.Packages {
			if value == "" || !utf8.ValidString(value) || strings.HasPrefix(value, "-") || strings.ContainsAny(value, "\x00\r\n") {
				return false
			}
		}
		return true
	case "setup":
		return p.Env == nil && p.Packages == nil && p.Network != "" && p.Command != "" && utf8.ValidString(p.Command) && !strings.ContainsRune(p.Command, 0)
	default:
		return false
	}
}
