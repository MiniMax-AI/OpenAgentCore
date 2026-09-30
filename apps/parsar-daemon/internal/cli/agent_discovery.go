package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudesdk"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/codex"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/mcode"
	opencodeagent "github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// agentCLIDiscovery is the daemon startup snapshot advertised in heartbeat.
type agentCLIDiscovery struct {
	installedKinds map[string]bool
	MCodeWorkspace *mcode.WorkspaceConfig
	ClaudeSDK      *claudeSDKDiscovery
	ClaudeCode     proto.SupportedAgentKind
	OpenCode       proto.SupportedAgentKind
	Codex          proto.SupportedAgentKind
	Pi             proto.SupportedAgentKind
	MCode          proto.SupportedAgentKind
}

func (d agentCLIDiscovery) permits(kind string) bool {
	return d.installedKinds == nil || d.installedKinds[kind]
}

type agentCLIChecks struct {
	ClaudeSDK  func(context.Context, claudesdk.Config) (claudesdk.RuntimeInfo, error)
	ClaudeCode func(context.Context, string) (string, error)
	OpenCode   func(context.Context, string) (string, error)
	Codex      func(context.Context, string) (string, error)
	Pi         func(context.Context, string) (string, error)
	MCode      func(context.Context, string) (string, error)
}

func defaultAgentCLIChecks() agentCLIChecks {
	return agentCLIChecks{
		ClaudeCode: claudecode.CheckCLIAvailable,
		OpenCode:   opencodeagent.CheckCLIAvailable,
		Codex:      codex.CheckCLIAvailable,
		Pi:         pi.CheckCLIAvailable,
		MCode:      mcode.CheckCLIAvailable,
	}
}

func preflightAgentCLIs(parent context.Context, rc *runContext, profile string) (agentCLIDiscovery, error) {
	return discoverAgentCLIs(parent, rc, profile, defaultAgentCLIChecks())
}

func discoverAgentCLIs(parent context.Context, rc *runContext, profile string, checks agentCLIChecks) (agentCLIDiscovery, error) {
	if checks.ClaudeCode == nil {
		checks.ClaudeCode = claudecode.CheckCLIAvailable
	}
	if checks.OpenCode == nil {
		checks.OpenCode = opencodeagent.CheckCLIAvailable
	}
	if checks.Codex == nil {
		checks.Codex = codex.CheckCLIAvailable
	}
	if checks.Pi == nil {
		checks.Pi = pi.CheckCLIAvailable
	}
	out := agentCLIDiscovery{
		installedKinds: rc.installedKinds,
		ClaudeCode: proto.SupportedAgentKind{
			Kind: "claude_code",
			Capabilities: proto.AgentKindCapabilities{
				SubagentObservations:           proto.CapabilityUnsupported,
				Streaming:                      proto.CapabilitySupported,
				Permissions:                    proto.CapabilitySupported,
				Usage:                          proto.CapabilitySupported,
				Resume:                         proto.CapabilitySupported,
				NativeSessionRecovery:          proto.CapabilityUnsupported,
				WorkspaceAuthoring:             proto.CapabilityUnsupported,
				Steering:                       proto.CapabilityUnsupported,
				MessageItems:                   proto.CapabilityUnsupported,
				ToolObservations:               proto.CapabilityUnsupported,
				EnvironmentNone:                proto.CapabilityUnsupported,
				LocalEnvironment:               proto.CapabilityUnsupported,
				Preparation:                    proto.CapabilityUnsupported,
				WorkspaceReadPreparation:       proto.CapabilityUnsupported,
				WorkspaceOutputExport:          proto.CapabilityUnsupported,
				ProgrammaticToolCallingDisable: proto.CapabilityUnsupported,
				WebSearchControl:               proto.CapabilityUnsupported,
				ExecutionControls:              proto.CapabilityUnsupported,
				TextVerbosity:                  proto.CapabilityUnsupported,
				StructuredOutput:               proto.CapabilityUnsupported,
				ToolSearch:                     proto.CapabilityUnsupported,
				MessageImages:                  proto.CapabilityUnsupported,
				FunctionResultImages:           proto.CapabilityUnsupported,
				SubagentControl:                proto.CapabilityUnsupported,
				DurableInputReceipts:           proto.CapabilityUnsupported,
				DurableTurns:                   proto.CapabilityUnsupported,
				FunctionTools:                  proto.CapabilityUnsupported,
				MCPHTTPTools:                   proto.CapabilityUnsupported,
				MCPHTTPRequired:                proto.CapabilityUnsupported,
				MCPHTTPBearerAuth:              proto.CapabilityUnsupported,
			},
		},
		OpenCode: proto.SupportedAgentKind{
			Kind: "opencode",
			Capabilities: proto.AgentKindCapabilities{
				SubagentObservations:           proto.CapabilityUnsupported,
				Streaming:                      proto.CapabilitySupported,
				Permissions:                    proto.CapabilityUnsupported,
				Usage:                          proto.CapabilitySupported,
				Resume:                         proto.CapabilityUnsupported,
				NativeSessionRecovery:          proto.CapabilityUnsupported,
				WorkspaceAuthoring:             proto.CapabilityUnsupported,
				Steering:                       proto.CapabilityUnsupported,
				MessageItems:                   proto.CapabilityUnsupported,
				ToolObservations:               proto.CapabilityUnsupported,
				EnvironmentNone:                proto.CapabilityUnsupported,
				LocalEnvironment:               proto.CapabilityUnsupported,
				Preparation:                    proto.CapabilityUnsupported,
				WorkspaceReadPreparation:       proto.CapabilityUnsupported,
				WorkspaceOutputExport:          proto.CapabilityUnsupported,
				ProgrammaticToolCallingDisable: proto.CapabilityUnsupported,
				WebSearchControl:               proto.CapabilityUnsupported,
				ExecutionControls:              proto.CapabilityUnsupported,
				TextVerbosity:                  proto.CapabilityUnsupported,
				StructuredOutput:               proto.CapabilityUnsupported,
				ToolSearch:                     proto.CapabilityUnsupported,
				MessageImages:                  proto.CapabilityUnsupported,
				FunctionResultImages:           proto.CapabilityUnsupported,
				SubagentControl:                proto.CapabilityUnsupported,
				DurableInputReceipts:           proto.CapabilityUnsupported,
				DurableTurns:                   proto.CapabilityUnsupported,
				FunctionTools:                  proto.CapabilityUnsupported,
				MCPHTTPTools:                   proto.CapabilityUnsupported,
				MCPHTTPRequired:                proto.CapabilityUnsupported,
				MCPHTTPBearerAuth:              proto.CapabilityUnsupported,
			},
		},
		Codex: proto.SupportedAgentKind{
			Kind: "codex",
			Capabilities: proto.AgentKindCapabilities{
				SubagentObservations:           proto.CapabilitySupported,
				Streaming:                      proto.CapabilitySupported,
				Permissions:                    proto.CapabilitySupported,
				Usage:                          proto.CapabilitySupported,
				Resume:                         proto.CapabilitySupported,
				NativeSessionRecovery:          proto.CapabilityUnsupported,
				WorkspaceAuthoring:             proto.CapabilityUnsupported,
				Steering:                       proto.CapabilitySupported,
				MessageItems:                   proto.CapabilitySupported,
				ToolObservations:               proto.CapabilitySupported,
				EnvironmentNone:                proto.CapabilitySupported,
				LocalEnvironment:               proto.CapabilityUnsupported,
				Preparation:                    proto.CapabilityUnsupported,
				WorkspaceReadPreparation:       proto.CapabilityUnsupported,
				WorkspaceOutputExport:          proto.CapabilityUnsupported,
				ProgrammaticToolCallingDisable: proto.CapabilitySupported,
				WebSearchControl:               proto.CapabilitySupported,
				ExecutionControls:              proto.CapabilityFromBool(codex.SupportsTextVerbosity),
				TextVerbosity:                  proto.CapabilityFromBool(codex.SupportsTextVerbosity),
				StructuredOutput:               proto.CapabilityUnsupported,
				ToolSearch:                     proto.CapabilityUnsupported,
				MessageImages:                  proto.CapabilitySupported,
				FunctionResultImages:           proto.CapabilitySupported,
				SubagentControl:                proto.CapabilitySupported,
				DurableInputReceipts:           proto.CapabilitySupported,
				DurableTurns:                   proto.CapabilitySupported,
				FunctionTools:                  proto.CapabilitySupported,
				MCPHTTPTools:                   proto.CapabilitySupported,
				MCPHTTPRequired:                proto.CapabilityUnsupported,
				MCPHTTPBearerAuth:              proto.CapabilitySupported,
			},
		},
		Pi: proto.SupportedAgentKind{
			Kind: "pi",
			Capabilities: proto.AgentKindCapabilities{
				SubagentObservations:           proto.CapabilityUnsupported,
				Streaming:                      proto.CapabilitySupported,
				Permissions:                    proto.CapabilityUnsupported,
				Usage:                          proto.CapabilitySupported,
				Resume:                         proto.CapabilitySupported,
				NativeSessionRecovery:          proto.CapabilityUnsupported,
				WorkspaceAuthoring:             proto.CapabilityUnsupported,
				Steering:                       proto.CapabilityUnsupported,
				MessageItems:                   proto.CapabilityUnsupported,
				ToolObservations:               proto.CapabilityUnsupported,
				EnvironmentNone:                proto.CapabilityUnsupported,
				LocalEnvironment:               proto.CapabilityUnsupported,
				Preparation:                    proto.CapabilityUnsupported,
				WorkspaceReadPreparation:       proto.CapabilityUnsupported,
				WorkspaceOutputExport:          proto.CapabilityUnsupported,
				ProgrammaticToolCallingDisable: proto.CapabilityUnsupported,
				WebSearchControl:               proto.CapabilityUnsupported,
				ExecutionControls:              proto.CapabilityUnsupported,
				TextVerbosity:                  proto.CapabilityUnsupported,
				StructuredOutput:               proto.CapabilityUnsupported,
				ToolSearch:                     proto.CapabilityUnsupported,
				MessageImages:                  proto.CapabilityUnsupported,
				FunctionResultImages:           proto.CapabilityUnsupported,
				SubagentControl:                proto.CapabilityUnsupported,
				DurableInputReceipts:           proto.CapabilityUnsupported,
				DurableTurns:                   proto.CapabilityUnsupported,
				FunctionTools:                  proto.CapabilityUnsupported,
				MCPHTTPTools:                   proto.CapabilityUnsupported,
				MCPHTTPRequired:                proto.CapabilityUnsupported,
				MCPHTTPBearerAuth:              proto.CapabilityUnsupported,
			},
		},
	}

	if out.permits("claude_code") {
		claudeCtx, cancelClaude := context.WithTimeout(parent, cliVersionTimeout)
		claudeVersion, claudeErr := checks.ClaudeCode(claudeCtx, "")
		cancelClaude()
		if claudeErr == nil {
			out.ClaudeCode.Available = true
			out.ClaudeCode.Version = claudeVersion
			fmt.Fprintf(rc.stdout, "Claude Code preflight ok (%s)\n", claudeVersion)
		} else if errors.Is(claudeErr, claudecode.ErrCLINotFound) {
			fmt.Fprintln(rc.stderr, "oac-daemon: Claude Code CLI not found on PATH; claude_code unavailable.")
			fmt.Fprintf(rc.stderr, "  Install instructions: %s\n", claudecode.InstallURL)
		} else {
			fmt.Fprintf(rc.stderr, "oac-daemon: `claude --version` failed; claude_code unavailable: %v\n", claudeErr)
			fmt.Fprintf(rc.stderr, "  Re-install or upgrade: %s\n", claudecode.InstallURL)
		}
	}

	if out.permits("opencode") {
		opencodeCtx, cancelOpenCode := context.WithTimeout(parent, cliVersionTimeout)
		opencodeVersion, opencodeErr := checks.OpenCode(opencodeCtx, "")
		cancelOpenCode()
		if opencodeErr == nil {
			out.OpenCode.Available = true
			out.OpenCode.Version = opencodeVersion
			fmt.Fprintf(rc.stdout, "OpenCode preflight ok (%s)\n", opencodeVersion)
		} else if errors.Is(opencodeErr, opencodeagent.ErrCLINotFound) {
			fmt.Fprintln(rc.stderr, "oac-daemon: OpenCode CLI not found on PATH; opencode unavailable.")
			fmt.Fprintf(rc.stderr, "  Install instructions: %s\n", opencodeagent.InstallURL)
		} else {
			fmt.Fprintf(rc.stderr, "oac-daemon: `opencode --version` failed; opencode unavailable: %v\n", opencodeErr)
			fmt.Fprintf(rc.stderr, "  Re-install or upgrade: %s\n", opencodeagent.InstallURL)
		}
	}

	if out.permits("codex") {
		codexCtx, cancelCodex := context.WithTimeout(parent, cliVersionTimeout)
		codexVersion, codexErr := checks.Codex(codexCtx, "")
		cancelCodex()
		if codexErr == nil {
			out.Codex.Available = true
			out.Codex.Version = codexVersion
			out.Codex.Capabilities.NativeSessionRecovery = proto.CapabilityFromBool(codex.SupportsNativeSessionRecovery(codexVersion))
			out.Codex.Capabilities.LocalEnvironment = proto.CapabilityFromBool(codex.SupportsLocalEnvironment(codexVersion))
			out.Codex.Capabilities.MCPHTTPRequired = proto.CapabilityFromBool(codex.SupportsNativeSessionRecovery(codexVersion))
			fmt.Fprintf(rc.stdout, "Codex preflight ok (%s)\n", codexVersion)
		} else if errors.Is(codexErr, codex.ErrCLINotFound) {
			fmt.Fprintln(rc.stderr, "oac-daemon: Codex CLI not found on PATH; codex unavailable.")
			fmt.Fprintf(rc.stderr, "  Install instructions: %s\n", codex.InstallURL)
		} else {
			fmt.Fprintf(rc.stderr, "oac-daemon: `codex --version` failed; codex unavailable: %v\n", codexErr)
			fmt.Fprintf(rc.stderr, "  Re-install or upgrade: %s\n", codex.InstallURL)
		}
	}

	if out.permits("pi") {
		piCtx, cancelPi := context.WithTimeout(parent, cliVersionTimeout)
		piVersion, piErr := checks.Pi(piCtx, "")
		cancelPi()
		if piErr == nil {
			out.Pi.Available = true
			out.Pi.Version = piVersion
			fmt.Fprintf(rc.stdout, "pi preflight ok (%s)\n", piVersion)
		} else if errors.Is(piErr, pi.ErrCLINotFound) {
			fmt.Fprintln(rc.stderr, "oac-daemon: pi CLI not found on PATH; pi unavailable.")
			fmt.Fprintf(rc.stderr, "  Install instructions: %s\n", pi.InstallURL)
		} else {
			fmt.Fprintf(rc.stderr, "oac-daemon: `pi --version` failed; pi unavailable: %v\n", piErr)
			fmt.Fprintf(rc.stderr, "  Re-install or upgrade: %s\n", pi.InstallURL)
		}
	}

	if out.permits("mcode") {
		out.MCode = discoverMCode(parent, rc, checks.MCode)
		discoverMCodeWorkspace(parent, rc, &out)
	}
	if out.permits("claude_sdk") {
		out.ClaudeSDK = discoverClaudeSDK(parent, rc, profile, checks.ClaudeSDK)
	}

	if err := parent.Err(); err != nil {
		return out, err
	}
	if !out.ClaudeCode.Available && !out.OpenCode.Available && !out.Codex.Available && !out.Pi.Available && !out.MCode.Available && (out.ClaudeSDK == nil || !out.ClaudeSDK.Info.Available) {
		return out, fmt.Errorf("connect: no supported agent CLI available (install Claude Code, OpenCode, Codex, pi, or mcode, or configure a Claude SDK runtime)")
	}
	return out, nil
}
