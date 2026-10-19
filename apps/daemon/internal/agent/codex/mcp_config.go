package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// mcpServerConfig is the daemon-internal MCP server config resolved from
// the request's MCP bindings. Written into <CODEX_HOME>/config.toml
// before spawning the app-server child.
type mcpServerConfig struct {
	Name              string
	URL               string
	Command           string
	Args              []string
	Cwd               string
	EnvVars           []string
	EnabledTools      *[]string
	Required          bool
	BearerTokenEnvVar string
	HTTPHeaders       map[string]string
}

// writeCodexMCPConfig writes a `[mcp_servers.<name>]` TOML table per
// server into <codexHome>/config.toml. Servers are sorted by name so
// the file is deterministic and diffable.
//
// Appends to the file rather than truncating because
// writeCodexProviderConfig writes to the same path. Both writers run
// once per prompt after resetGeneratedConfig; native history stays in CODEX_HOME.
// The transport and enabled_tools fields mirror native McpServerConfig.
func writeCodexMCPConfig(codexHome string, servers map[string]mcpServerConfig) error {
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return fmt.Errorf("codex: mkdir CODEX_HOME %s: %w", codexHome, err)
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		srv := servers[name]
		// TOML table name. Use a quoted key form for safety against
		// names that contain dots / dashes / unicode.
		b.WriteString("[mcp_servers.")
		b.WriteString(tomlQuoteString(name))
		b.WriteString("]\n")
		if srv.Required {
			b.WriteString("required = true\n")
		}
		if srv.URL != "" {
			b.WriteString(`url = `)
			b.WriteString(tomlQuoteString(srv.URL))
			b.WriteByte('\n')
			if srv.BearerTokenEnvVar != "" {
				b.WriteString("bearer_token_env_var = ")
				b.WriteString(tomlQuoteString(srv.BearerTokenEnvVar))
				b.WriteByte('\n')
			}
			writeMCPHeaderMap(&b, "http_headers", srv.HTTPHeaders)
		} else {
			b.WriteString(`command = `)
			b.WriteString(tomlQuoteString(srv.Command))
			b.WriteByte('\n')
			writeMCPList(&b, "args", srv.Args)
			if srv.Cwd != "" {
				b.WriteString("cwd = " + tomlQuoteString(srv.Cwd) + "\n")
			}
			writeMCPList(&b, "env_vars", srv.EnvVars)
		}
		if srv.EnabledTools != nil {
			b.WriteString("enabled_tools = ")
			writeMCPList(&b, "", *srv.EnabledTools)
		}
		b.WriteByte('\n')
	}

	path := filepath.Join(codexHome, "config.toml")
	return appendConfigTOML(path, b.String())
}

// writeMCPList writes a TOML string array, as a "name = " line unless name is empty.
func writeMCPList(b *strings.Builder, name string, values []string) {
	if name != "" {
		if len(values) == 0 {
			return
		}
		b.WriteString(name + " = ")
	}
	b.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(tomlQuoteString(value))
	}
	b.WriteString("]\n")
}

func writeMCPHeaderMap(b *strings.Builder, field string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	b.WriteString(field + " = {")
	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(tomlQuoteString(key) + " = " + tomlQuoteString(values[key]))
	}
	b.WriteString("}\n")
}

// tomlQuoteString returns a TOML basic-string literal (double-quoted)
// with the documented escape set applied — \" \\ \n \r \t plus
// \uXXXX for the other control characters, DEL included. Matches the TOML 1.0
// spec for basic strings.
func tomlQuoteString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
