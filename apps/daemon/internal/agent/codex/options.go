package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	harnessconfiguration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/codex"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// SessionPlan holds the resolved per-prompt launch plan derived from
// the daemon's PromptRequestPayload.
type SessionPlan struct {
	// Cwd is the working directory passed to codex and the spawned
	// app-server: the bound workspace root for an Environment request and,
	// for environment:none, the Session's private CODEX_HOME or a view's work
	// directory.
	Cwd string

	// Env is the environment slice (KEY=value) the plan adds. A local
	// codex layers it onto os.Environ(); in an agent-host view it is the
	// complete environment. Includes CODEX_HOME.
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

	// home is CODEX_HOME as the daemon writes it and as codex sees it.
	home agent.ViewDir

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

	// Cleanup is the deferred housekeeping the session must run after the child exits.
	Cleanup func()
}

// BuildSessionPlan derives a SessionPlan from the request's frozen model
// configuration and ExecutionControls. The codex binary is resolved via PATH.
func BuildSessionPlan(req proto.PromptRequestPayload) (SessionPlan, error) {
	return buildSessionPlan(req, func() (agent.ViewDir, error) {
		home, err := allocCodexHome(req.AgentStateKey)
		return agent.ViewDir{Host: home, View: home}, err
	})
}

// buildSessionPlan derives the plan with CODEX_HOME from allocHome, which runs
// only after the request validates.
func buildSessionPlan(req proto.PromptRequestPayload, allocHome func() (agent.ViewDir, error)) (SessionPlan, error) {
	plan := SessionPlan{
		// Harnesses run unattended: Codex never offers its ask-the-user tool.
		ExtraConfig: [][2]string{{"tools.experimental_request_user_input.enabled", "false"}},
		Cleanup:     func() {},
	}
	prepared, err := harnessconfiguration.Configuration().Prepare(req)
	if err != nil {
		return plan, err
	}
	if controls := req.ExecutionControls; controls != nil {
		switch controls.WebSearch {
		case "disabled", "cached", "live":
		default:
			return plan, fmt.Errorf("codex: web_search must be disabled, cached or live")
		}
		switch controls.TextVerbosity {
		case "low", "medium", "high":
		default:
			return plan, fmt.Errorf("codex: text_verbosity must be low, medium or high")
		}
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"web_search", strconv(controls.WebSearch)}, [2]string{"model_verbosity", strconv(controls.TextVerbosity)})
	}
	plan.Model = prepared.Model
	plan.SystemPrompt = req.SystemPrompt

	home, err := allocHome()
	if err != nil {
		return plan, err
	}
	if err := resetGeneratedConfig(home.Host); err != nil {
		return plan, err
	}
	plan.home = home
	plan.Env = []string{"DISABLE_TELEMETRY=1", "CODEX_HOME=" + home.View}
	if req.DisableExecutionEnvironment {
		plan.Env = append(plan.Env, "CODEX_EXEC_SERVER_URL=none")
	}
	if prepared.Provider != nil {
		if err := writeCodexProviderConfig(home.Host, nativeProvider(*prepared.Provider)); err != nil {
			return plan, err
		}
		plan.ModelProvider = oacProviderSlug
	}
	if effort, ok := prepared.HarnessConfig["model_reasoning_effort"].(string); ok {
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

// openNativeHome opens CODEX_HOME from its parent. Every read and write in the
// home goes through this Root: the Session user owns a view home, and a link
// it leaves there resolves only inside the parent, never outside it.
func openNativeHome(codexHome string) (*os.Root, error) {
	if !filepath.IsAbs(codexHome) {
		return nil, errors.New("codex: missing private native home")
	}
	parent, err := os.OpenRoot(filepath.Dir(codexHome))
	if err != nil {
		return nil, fmt.Errorf("codex: open native home: %w", err)
	}
	defer parent.Close()
	root, err := parent.OpenRoot(filepath.Base(codexHome))
	if err != nil {
		return nil, fmt.Errorf("codex: open native home: %w", err)
	}
	return root, nil
}

// resetGeneratedConfig removes config.toml; a link in its place is removed,
// never followed.
func resetGeneratedConfig(codexHome string) error {
	root, err := openNativeHome(codexHome)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove("config.toml"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("codex: remove generated config: %w", err)
	}
	return nil
}

// nativeProvider renders the frozen model provider as Codex's provider entry.
func nativeProvider(provider modelprovider.Provider) providerConfig {
	return providerConfig{BaseURL: provider.BaseURL, BearerToken: provider.APIKey, WireAPI: "responses"}
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
