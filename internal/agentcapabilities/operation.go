package agentcapabilities

import "github.com/MiniMax-AI-Dev/parsar/internal/agentplugin"

// Operation is confidential stdin for the packaged helper, not a public API.
type Operation struct {
	Version int                  `json:"version"`
	Action  string               `json:"action"`
	Slot    int                  `json:"slot,omitempty"`
	Archive []byte               `json:"archive,omitempty"`
	Plugin  agentplugin.Metadata `json:"plugin,omitempty"`
	Sources Input                `json:"sources,omitempty"`
}
