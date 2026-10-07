package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	harnessconfiguration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/codex"
)

// SessionPlan holds the resolved per-prompt launch plan derived from
// the daemon's PromptRequestPayload.
type SessionPlan struct {
	// Cwd is the working directory passed to codex and the spawned
	// app-server: the bound workspace root for an Environment request and
	// the Session's private CODEX_HOME for environment:none.
	Cwd string

	// Env is the full environment slice (KEY=value) to layer onto
	// os.Environ() before spawning. Includes CODEX_HOME and the entries
	// of the env option.
	Env []string

	// ExtraConfig is a list of `-c key=value` overrides applied at the
	// app-server CLI.
	ExtraConfig [][2]string

	// EnableFeatures / DisableFeatures forward to `--enable / --disable`
	// flags for the profiles the adapter configures.
	EnableFeatures  []string
	DisableFeatures []string

	// Non-nil for declared service or Environment MCP, including private references.
	mcpServers map[string]mcpServerConfig

	// Model is the slug to request on thread/start. Empty inherits the
	// codex.config.toml default.
	Model string

	// ModelProvider is the slug pinned on thread/start so codex routes
	// the prompt through the [model_providers.<slug>] entry we wrote
	// into <CODEX_HOME>/config.toml. Empty leaves codex on its builtin
	// "openai" provider (only valid when the caller really wants
	// public api.openai.com + OPENAI_API_KEY env), so the normal path is
	// oacProviderSlug.
	ModelProvider string

	// SystemPrompt is forwarded as developerInstructions on thread/start.
	SystemPrompt string

	// ModelReasoningEffort is frozen for launch and every native Turn.
	ModelReasoningEffort string

	// ApprovalPolicy + Sandbox apply to both new and resumed threads.
	ApprovalPolicy AskForApproval
	Sandbox        SandboxMode
	Permissions    string

	// Cleanup is the deferred housekeeping the session must run after the child exits.
	Cleanup func()
}

// BuildSessionPlan derives a SessionPlan from the request's agent_options and
// execution controls. It reads these agent_options keys:
//
//	model           string  codex model slug, e.g. "gpt-5.5"
//	system_prompt   string  forwarded as developerInstructions
//	model_provider  object  frozen upstream protocol, endpoint, credential and token limits
//	harness_config  object  native parameters accepted by harnessconfig/codex
//
// Execution controls select the native web_search and model_verbosity settings.
// The codex binary itself is resolved via PATH only.
//
// Daemon-managed Codex sessions bypass approvals and the engine sandbox.
func BuildSessionPlan(agentStateKey string, opts map[string]any, controls *proto.ExecutionControls) (SessionPlan, error) {
	plan := SessionPlan{
		ApprovalPolicy: AskForApproval{String: "never"},
		Sandbox:        SandboxDangerFullAcces,
		Cleanup:        func() {},
	}

	nativeConfig, err := harnessconfiguration.Configuration().PrepareHarnessConfig(opts)
	if err != nil {
		return plan, err
	}

	if value, present := opts["model_provider"]; present && (value == nil || stringOpt(opts, "model") == "") {
		return plan, errors.New("codex: model and complete model_provider are required")
	}

	if controls != nil {
		if !slices.Contains([]string{"disabled", "cached", "live"}, controls.WebSearch) {
			return plan, errors.New("codex: web_search must be disabled, cached or live")
		}
		if !slices.Contains([]string{"low", "medium", "high"}, controls.TextVerbosity) {
			return plan, errors.New("codex: text_verbosity must be low, medium or high")
		}
	}

	plan.Model = stringOpt(opts, "model")
	plan.SystemPrompt = stringOpt(opts, "system_prompt")

	codexHome, err := allocCodexHome(agentStateKey)
	if err != nil {
		return plan, err
	}
	if err := resetGeneratedConfig(codexHome); err != nil {
		return plan, err
	}
	env := []string{"DISABLE_TELEMETRY=1", "CODEX_HOME=" + codexHome}

	provider, hasProvider, err := normaliseProviderConfig(opts["model_provider"])
	if err != nil {
		return plan, err
	}
	if hasProvider {
		if err := writeCodexProviderConfig(codexHome, provider); err != nil {
			return plan, err
		}
		plan.ModelProvider = oacProviderSlug
	}

	plan.Env = env
	if controls != nil {
		plan.ExtraConfig = [][2]string{{"web_search", strconv(controls.WebSearch)}, {"model_verbosity", strconv(controls.TextVerbosity)}}
	}
	if effort, ok := nativeConfig["model_reasoning_effort"].(string); ok {
		plan.ModelReasoningEffort = effort
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"model_reasoning_effort", strconv(effort)})
	}
	if plan.ModelProvider != "" {
		// Pin model_provider at the CLI layer so codex skips its builtin
		// "openai" provider — without this the [model_providers.oac]
		// block we wrote into config.toml would be loaded but never
		// selected (the default model_provider is "openai").
		plan.ExtraConfig = append(plan.ExtraConfig,
			[2]string{"model_provider", strconv(plan.ModelProvider)})
	}
	return plan, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func allocCodexHome(agentStateKey string) (string, error) {
	if strings.TrimSpace(agentStateKey) == "" {
		return "", fmt.Errorf("codex: agentStateKey required for CODEX_HOME allocation")
	}
	root, err := paths.Root()
	if err != nil {
		return "", err
	}
	parts := strings.Split(agentStateKey, "/")
	safeParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if safe := safePathPartCodex(part); safe != "" {
			safeParts = append(safeParts, safe)
		}
	}
	if len(safeParts) == 0 {
		return "", fmt.Errorf("codex: invalid agentStateKey %q", agentStateKey)
	}
	dirParts := append([]string{root, "daemon", "agent-sessions"}, safeParts...)
	dir := filepath.Join(dirParts...)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("codex: create CODEX_HOME %s: %w", dir, err)
	}
	return dir, nil
}

// nativeHomeFromPlan returns the CODEX_HOME codex receives: os/exec keeps the
// last duplicate entry, and BuildSessionPlan appends the allocated home last.
func nativeHomeFromPlan(plan SessionPlan) string {
	var home string
	for _, entry := range plan.Env {
		if value, ok := strings.CutPrefix(entry, "CODEX_HOME="); ok {
			home = value
		}
	}
	return home
}

func resetGeneratedConfig(codexHome string) error {
	path := filepath.Join(codexHome, "config.toml")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("codex: remove generated config %s: %w", path, err)
	}
	return nil
}

// normaliseProviderConfig validates and renders the frozen native provider.
func normaliseProviderConfig(raw any) (providerConfig, bool, error) {
	if raw == nil {
		return providerConfig{}, false, nil
	}
	provider, err := harnessconfiguration.Configuration().ParseProvider(raw)
	if err != nil {
		return providerConfig{}, false, err
	}
	return providerConfig{BaseURL: provider.BaseURL, BearerToken: provider.APIKey, WireAPI: "responses"}, true, nil
}

func stringOpt(opts map[string]any, key string) string {
	if opts == nil {
		return ""
	}
	v, ok := opts[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func safePathPartCodex(runID string) string {
	var b strings.Builder
	for _, r := range runID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "run"
	}
	return out
}

// strconv quotes a value as a TOML string. Done by reusing the JSON
// encoder for escape rules — TOML strings accept the same standard
// escape set so this is wire-safe.
func strconv(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}
