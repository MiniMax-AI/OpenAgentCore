package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// This table is local to the daemon because Runtime distributions are standalone.
var renamedRuntimeSettings = map[string]string{
	"PARSAR_CLAUDE_BIN":                 "OAC_RUNTIME_CLAUDE_BIN",
	"PARSAR_CLAUDE_SDK_ENTRYPOINT":      "OAC_RUNTIME_CLAUDE_SDK_ENTRYPOINT",
	"PARSAR_CLAUDE_SDK_NODE":            "OAC_RUNTIME_CLAUDE_SDK_NODE",
	"PARSAR_CLAUDE_SDK_WORKSPACE":       "OAC_RUNTIME_CLAUDE_SDK_WORKSPACE",
	"PARSAR_CODEX_BIN":                  "OAC_RUNTIME_CODEX_BIN",
	"PARSAR_CODEX_HARNESS_BIN":          "OAC_RUNTIME_CODEX_HARNESS_BIN",
	"PARSAR_CODEX_PERMISSION_PROFILE":   "OAC_RUNTIME_CODEX_PERMISSION_PROFILE",
	"PARSAR_DAEMON_BACKGROUND_CHILD":    "OAC_RUNTIME_DAEMON_BACKGROUND_CHILD",
	"PARSAR_DAEMON_CONNECT_DEVICE_NAME": "OAC_RUNTIME_DAEMON_CONNECT_DEVICE_NAME",
	"PARSAR_DAEMON_CONNECT_TOKEN":       "OAC_RUNTIME_DAEMON_CONNECT_TOKEN",
	"PARSAR_DAEMON_CONNECT_URL":         "OAC_RUNTIME_DAEMON_CONNECT_URL",
	"PARSAR_DAEMON_SUSPEND_PID_FILE":    "OAC_RUNTIME_DAEMON_SUSPEND_PID_FILE",
	"PARSAR_DAEMON_SOCKET":              "OAC_RUNTIME_DAEMON_SOCKET",
	"PARSAR_HOME":                       "OAC_RUNTIME_HOME",
	"PARSAR_MCODE_AGENTS_API":           "OAC_RUNTIME_MCODE_AGENTS_API",
	"PARSAR_MCODE_BIN":                  "OAC_RUNTIME_MCODE_BIN",
	"PARSAR_MCODE_MAX_SUBAGENTS":        "OAC_RUNTIME_MCODE_MAX_SUBAGENTS",
	"PARSAR_MCODE_NODE":                 "OAC_RUNTIME_MCODE_NODE",
	"PARSAR_MCODE_TOOL_POLICY":          "OAC_RUNTIME_MCODE_TOOL_POLICY",
	"PARSAR_MCODE_WORKSPACE":            "OAC_RUNTIME_MCODE_WORKSPACE",
	"PARSAR_MCODE_WORKSPACE_BRIDGE":     "OAC_RUNTIME_MCODE_WORKSPACE_BRIDGE",
	"PARSAR_OPENCODE_BIN":               "OAC_RUNTIME_OPENCODE_BIN",
	"PARSAR_PI_API_KEY":                 "OAC_RUNTIME_PI_API_KEY",
	"PARSAR_PI_BIN":                     "OAC_RUNTIME_PI_BIN",
	"PARSAR_RUNTIME_ALLOWED_DOMAINS":    "OAC_RUNTIME_ALLOWED_DOMAINS",
	"PARSAR_RUNTIME_DIRECTORY_HELPER":   "OAC_RUNTIME_DIRECTORY_HELPER",
	"PARSAR_RUNTIME_ENVIRONMENT_ID":     "OAC_RUNTIME_ENVIRONMENT_ID",
	"PARSAR_RUNTIME_EXPORT_HELPER":      "OAC_RUNTIME_EXPORT_HELPER",
	"PARSAR_RUNTIME_NETWORK_ACCESS":     "OAC_RUNTIME_NETWORK_ACCESS",
	"PARSAR_RUNTIME_SESSION_ID":         "OAC_RUNTIME_SESSION_ID",
	"PARSAR_RUNTIME_STAGING":            "OAC_RUNTIME_STAGING",
	"PARSAR_RUNTIME_SYSTEM_PACKAGES":    "OAC_RUNTIME_SYSTEM_PACKAGES",
	"PARSAR_RUNTIME_TOOL_ENV":           "OAC_RUNTIME_TOOL_ENV",
	"PARSAR_RUNTIME_TOOL_SCRATCH":       "OAC_RUNTIME_TOOL_SCRATCH",
	"PARSAR_RUNTIME_WORKSPACE":          "OAC_RUNTIME_WORKSPACE",
	"PARSAR_RUNTIME_WRITE_HELPER":       "OAC_RUNTIME_WRITE_HELPER",
	"PARSAR_LOG_LEVEL":                  "OAC_LOG_LEVEL",
	"PARSAR_LOG_FORMAT":                 "OAC_LOG_FORMAT",
	"PARSAR_LOG_ADD_SOURCE":             "OAC_LOG_ADD_SOURCE",
}

func validateRuntimeConfiguration() error {
	var diagnostics []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "PARSAR_") {
			continue
		}
		// Separate-product integration names are not Runtime settings.
		switch name {
		case "PARSAR_CAPABILITY_UPLOAD_TOKEN", "PARSAR_SERVER_URL", "PARSAR_MASTER_KEY", "PARSAR_PROTOCOL_BASELINE_REVISION":
			continue
		}
		replacement := renamedRuntimeSettings[name]
		if strings.HasPrefix(name, "PARSAR_MCP_BEARER_") || strings.HasPrefix(name, "PARSAR_MCP_HEADER_") {
			replacement = "OAC_RUNTIME_" + strings.TrimPrefix(name, "PARSAR_")
		}
		if replacement != "" {
			diagnostics = append(diagnostics, fmt.Sprintf("%s was renamed to %s; this Runtime image or launcher sets the old name. Use a Runtime built by this release.", name, replacement))
		} else {
			diagnostics = append(diagnostics, fmt.Sprintf("%s is not read by OpenAgentCore; OAC_RUNTIME_* replaced PARSAR_*. Remove it.", name))
		}
	}
	if len(diagnostics) == 0 {
		return nil
	}
	sort.Strings(diagnostics)
	return fmt.Errorf("%s", strings.Join(diagnostics, "\n"))
}
