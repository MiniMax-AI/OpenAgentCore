package pi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// modelKeyEnv carries the provider key to Pi without writing it to disk or
// exposing it in process arguments.
const modelKeyEnv = "OAC_PI_MODEL_KEY"

const providerID = "oac"

// protocolAPI maps the shared provider protocol vocabulary to Pi's native API
// names. Core never converts between protocols; an unmapped protocol is refused
// before native resources start.
var protocolAPI = map[modelprovider.Protocol]string{
	modelprovider.ChatCompletions: "openai-completions",
	modelprovider.Responses:       "openai-responses",
	modelprovider.Anthropic:       "anthropic-messages",
}

type modelsFile struct {
	Providers map[string]modelsProvider `json:"providers"`
}

type modelsProvider struct {
	Name    string        `json:"name"`
	BaseURL string        `json:"baseUrl"`
	APIKey  string        `json:"apiKey"`
	API     string        `json:"api"`
	Models  []modelsModel `json:"models"`
}

type modelsModel struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	ContextWindow int32  `json:"contextWindow,omitempty"`
	MaxTokens     int32  `json:"maxTokens,omitempty"`
}

// writeModelConfig renders the frozen Session model configuration into Pi's
// private agent directory. The provider key stays in the child environment.
func writeModelConfig(agentDir string, prepared harnessconfig.PreparedConfiguration) error {
	if prepared.Provider == nil {
		return fmt.Errorf("pi: a model provider is required")
	}
	api, ok := protocolAPI[prepared.Provider.Protocol]
	if !ok {
		return fmt.Errorf("pi: unsupported model provider protocol %q", prepared.Provider.Protocol)
	}
	file := modelsFile{Providers: map[string]modelsProvider{
		providerID: {
			Name:    providerID,
			BaseURL: prepared.Provider.BaseURL,
			APIKey:  "$" + modelKeyEnv,
			API:     api,
			Models: []modelsModel{{
				ID:            prepared.Model,
				Name:          prepared.Model,
				ContextWindow: prepared.Provider.ContextWindow,
				MaxTokens:     prepared.Provider.MaxOutputTokens,
			}},
		},
	}}
	body, err := json.Marshal(file)
	if err != nil {
		return fmt.Errorf("pi: encode model configuration: %w", err)
	}
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return fmt.Errorf("pi: prepare agent directory: %w", err)
	}
	path := filepath.Join(agentDir, "models.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("pi: write model configuration: %w", err)
	}
	return nil
}
