package processconfig

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

const agentHostIdentity = `{"runtime_id": "2f1c4a7e-9b3d-4e5f-8a6b-1c2d3e4f5a6b", "credential": "synthetic-credential"}`

// required sets the settings Load requires and returns a file writer.
func required(t *testing.T) func(name, content string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Setenv("OAC_PUBLIC_URL", "https://core.example")
	t.Setenv("OAC_DATABASE_URL", "postgres://core@database/core")
	t.Setenv("OAC_INSTALLATION_ID_FILE", write("installation.id", testInstallationID+"\n"))
	t.Setenv("OAC_AGENT_HOST_IDENTITY_FILE", write("identity.json", agentHostIdentity))
	t.Setenv("OAC_CREDENTIAL_KEY_FILE", write("credential.key", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x91}, 32))+"\n"))
	t.Setenv("OAC_CORE_KEY_DIGESTS_FILE", write("digests.json", `["`+strings.Repeat("ab", 32)+`"]`))
	return write
}

const testInstallationID = "8c5f4f5e-2c55-4c43-9a49-7f2f3f2d1d10"

// rejects asserts that Load fails, names variable and does not echo secret.
func rejects(t *testing.T, variable, secret string) {
	t.Helper()
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), variable) || (secret != "" && strings.Contains(err.Error(), secret)) {
		t.Fatalf("%s: %v", variable, err)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	required(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != "127.0.0.1:8091" || c.PublicOrigin.String() != "https://core.example" || c.InstallationID != testInstallationID || c.AgentHostID != "2f1c4a7e-9b3d-4e5f-8a6b-1c2d3e4f5a6b" || c.CredentialKey == nil || c.CoreKeys == nil ||
		c.ExecutionConcurrency != 4 || c.DefaultHarness != "codex" || strings.Join(c.Harnesses, ",") != "claude_sdk,codex,mcode" ||
		c.WriteAuditRetention != 90*24*time.Hour || c.OAuthTrustedOrigins != nil || c.NativeInstallers != "" || c.ProviderPaths.StateRoot != "/state" {
		t.Fatalf("%+v", c)
	}
	if h := c.RuntimeHistory; h.File != "" || h.Endpoint != "" || h.QueueCapacity != 256 || h.Timeout != 2*time.Second || h.SampleInterval != 30*time.Second {
		t.Fatalf("%+v", h)
	}
	t.Setenv("OAC_PROVIDER_ROOT", "/opt/oac")
	if c, err = Load(); err != nil || c.ProviderPaths.ArtifactRoot != "/opt/oac" || c.NativeInstallers != "/opt/oac/native-installers" {
		t.Fatal(c, err)
	}
}

func TestLoadRejectsInvalidValuesWithoutEchoingThem(t *testing.T) {
	write := required(t)
	for _, variable := range []string{"OAC_PUBLIC_URL", "OAC_DATABASE_URL", "OAC_INSTALLATION_ID_FILE", "OAC_AGENT_HOST_IDENTITY_FILE", "OAC_CREDENTIAL_KEY_FILE", "OAC_CORE_KEY_DIGESTS_FILE"} {
		t.Run(variable+" missing", func(t *testing.T) {
			t.Setenv(variable, "")
			rejects(t, variable, "")
		})
	}
	t.Setenv("OAC_CORE_KEY_DIGESTS_FILE", write("bad-digests.json", `["synthetic-secret"]`))
	rejects(t, "OAC_CORE_KEY_DIGESTS_FILE", "synthetic-secret")
	required(t)
	for variable, value := range map[string]string{
		"OAC_PUBLIC_URL":               "https://user:synthetic-secret@core.example",
		"OAC_EXECUTION_CONCURRENCY":    "synthetic-secret",
		"OAC_DEFAULT_HARNESS":          "synthetic-secret",
		"OAC_HARNESSES":                "codex,synthetic-secret",
		"OAC_WRITE_AUDIT_RETENTION":    "synthetic-secret",
		"OAC_OAUTH_TRUSTED_ORIGINS":    "https://synthetic-secret.example/token",
		"OAC_LOG_LEVEL":                "verbose",
		"OAC_INSTALLATION_ID_FILE":     write("bad.id", "synthetic-secret"),
		"OAC_AGENT_HOST_IDENTITY_FILE": write("bad-identity.json", `{"runtime_id": "synthetic-secret", "credential": "c"}`),
		"OAC_CREDENTIAL_KEY_FILE":      write("bad.key", "synthetic-secret"),
		"OAC_HISTORY_SETTINGS_FILE":    write("history.json", `{"secret":"synthetic-secret"}`),
	} {
		t.Run(variable, func(t *testing.T) {
			t.Setenv(variable, value)
			rejects(t, variable, "synthetic-secret")
		})
	}
}

func TestAgentHostIdentity(t *testing.T) {
	write := required(t)
	for _, content := range []string{
		`{"runtime_id": "2F1C4A7E-9B3D-4E5F-8A6B-1C2D3E4F5A6B", "credential": "synthetic-secret"}`,
		`{"runtime_id": "2f1c4a7e-9b3d-4e5f-8a6b-1c2d3e4f5a6b", "credential": " "}`,
		`{"runtime_id": "2f1c4a7e-9b3d-4e5f-8a6b-1c2d3e4f5a6b", "credential": "synthetic-secret", "name": "x"}`,
		`{"runtime_id": "2f1c4a7e-9b3d-4e5f-8a6b-1c2d3e4f5a6b", "credential": "synthetic-secret"} {}`,
	} {
		t.Setenv("OAC_AGENT_HOST_IDENTITY_FILE", write("identity.json", content))
		rejects(t, "OAC_AGENT_HOST_IDENTITY_FILE", "synthetic-secret")
	}
	t.Setenv("OAC_AGENT_HOST_IDENTITY_FILE", write("identity.json", agentHostIdentity+"\n"))
	c, err := Load()
	if err != nil || c.AgentHostID != "2f1c4a7e-9b3d-4e5f-8a6b-1c2d3e4f5a6b" || c.AgentHostCredentialHash != runtimedevice.HashCredential("synthetic-credential") {
		t.Fatal(c.AgentHostID, err)
	}
}

func TestPublicURLMustBeACanonicalOrigin(t *testing.T) {
	required(t)
	for _, value := range []string{"https://core.example", "https://core.example:8443", "http://127.0.0.1:8091", "http://core.example"} {
		t.Setenv("OAC_PUBLIC_URL", value)
		if c, err := Load(); err != nil || c.PublicOrigin.String() != value {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"https://core.example/", "https://Core.example", "wss://core.example", "https://core.example/v1"} {
		t.Setenv("OAC_PUBLIC_URL", value)
		rejects(t, "OAC_PUBLIC_URL", "")
	}
}

func TestExecutionConcurrencyAndAuditRetention(t *testing.T) {
	required(t)
	for _, value := range []string{"1", "1024"} {
		t.Setenv("OAC_EXECUTION_CONCURRENCY", value)
		if _, err := Load(); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"0", "-1", "1025", "1.5"} {
		t.Setenv("OAC_EXECUTION_CONCURRENCY", value)
		rejects(t, "OAC_EXECUTION_CONCURRENCY", "")
	}
	t.Setenv("OAC_EXECUTION_CONCURRENCY", "")
	t.Setenv("OAC_WRITE_AUDIT_RETENTION", "24h")
	if c, err := Load(); err != nil || c.WriteAuditRetention != 24*time.Hour {
		t.Fatal(c.WriteAuditRetention, err)
	}
	for _, value := range []string{"0", "30m", "-1h", "90d"} {
		t.Setenv("OAC_WRITE_AUDIT_RETENTION", value)
		rejects(t, "OAC_WRITE_AUDIT_RETENTION", "")
	}
}

func TestHarnessesDefaultToEveryQualifiedHarness(t *testing.T) {
	required(t)
	t.Setenv("OAC_HARNESSES", "mcode")
	if c, err := Load(); err != nil || strings.Join(c.Harnesses, ",") != "codex,mcode" {
		t.Fatal(c.Harnesses, err)
	}
}

func TestOAuthTrustedOrigins(t *testing.T) {
	required(t)
	t.Setenv("OAC_OAUTH_TRUSTED_ORIGINS", "https://issuer.example, https://10.0.0.1:9443")
	if c, err := Load(); err != nil || strings.Join(c.OAuthTrustedOrigins, ",") != "https://issuer.example,https://10.0.0.1:9443" {
		t.Fatal(c.OAuthTrustedOrigins, err)
	}
	for _, value := range []string{"http://issuer.example", "https://issuer.example/token", "https://issuer.example,", "https://user:secret@issuer.example"} {
		t.Setenv("OAC_OAUTH_TRUSTED_ORIGINS", value)
		rejects(t, "OAC_OAUTH_TRUSTED_ORIGINS", "")
	}
}

func TestCredentialKey(t *testing.T) {
	write := required(t)
	first, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	binding := credentialcrypto.Binding{TenantID: "tenant", VaultID: "vault", CredentialID: "credential", AuthType: "static_bearer", Destination: "https://example.invalid/mcp"}
	sealed, err := first.CredentialKey.Seal([]byte("opaque storage test"), binding)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.CredentialKey.Open(sealed, binding); err != nil || string(got) != "opaque storage test" {
		t.Fatal("persisted key did not recover ciphertext", err)
	}
	t.Setenv("OAC_CREDENTIAL_KEY_FILE", filepath.Join(t.TempDir(), "missing.key"))
	rejects(t, "OAC_CREDENTIAL_KEY_FILE", "")
	for _, content := range []string{"", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		t.Setenv("OAC_CREDENTIAL_KEY_FILE", write("short.key", content))
		rejects(t, "OAC_CREDENTIAL_KEY_FILE", "")
	}
}

func TestRuntimeHistoryFile(t *testing.T) {
	write := required(t)
	t.Setenv("OAC_HISTORY_SETTINGS_FILE", write("history.json", `{"transport":"otlp_http","endpoint":"http://127.0.0.1:4318/v1/metrics","insecure":true,"headers":{"X-Scope-OrgID":"operator-history"},"sample_interval_seconds":60}`))
	c, err := Load()
	if err != nil || c.RuntimeHistory.Endpoint != "http://127.0.0.1:4318/v1/metrics" || c.RuntimeHistory.SampleInterval != time.Minute || c.RuntimeHistory.QueueCapacity != 256 {
		t.Fatal(c.RuntimeHistory, err)
	}
	for name, config := range map[string]string{
		"unknown field":               `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","secret":"must-not-leak"}`,
		"implicit insecure":           `{"transport":"otlp_http","endpoint":"http://collector.example.test/v1/metrics"}`,
		"userinfo":                    `{"transport":"otlp_http","endpoint":"https://user:must-not-leak@collector.example.test/v1/metrics"}`,
		"header newline":              "{\"transport\":\"otlp_http\",\"endpoint\":\"https://collector.example.test/v1/metrics\",\"headers\":{\"Authorization\":\"Bearer must-not-leak\\n\"}}",
		"reserved header":             `{"transport":"otlp_http","endpoint":"https://collector.example.test/v1/metrics","headers":{"Host":"must-not-leak"}}`,
		"oversized queue":             `{"queue_capacity":4097}`,
		"oversized timeout":           `{"timeout_seconds":31}`,
		"too frequent sampling":       `{"sample_interval_seconds":4}`,
		"oversized sampling interval": `{"sample_interval_seconds":301}`,
		"transport without endpoint":  `{"transport":"otlp_http"}`,
		"trailing value":              `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("OAC_HISTORY_SETTINGS_FILE", write("invalid.json", config))
			if _, err := Load(); err == nil || !strings.HasPrefix(err.Error(), "OAC_HISTORY_SETTINGS_FILE: ") || strings.Contains(err.Error(), "must-not-leak") {
				t.Fatal(err)
			}
		})
	}
}

func TestSettingsReportEffectiveValuesAndHideHistory(t *testing.T) {
	write := required(t)
	t.Setenv("OAC_EXECUTION_CONCURRENCY", "8")
	t.Setenv("OAC_LOG_LEVEL", "warn")
	t.Setenv("OAC_WRITE_AUDIT_RETENTION", "1440m")
	t.Setenv("OAC_HISTORY_SETTINGS_FILE", write("history.json", `{}`))
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]any{}
	for _, setting := range c.Settings() {
		if setting.Sensitive && (setting.Value != nil || setting.Configured == nil) {
			t.Fatalf("sensitive setting %s leaked a value", setting.Key)
		}
		found[setting.Key] = setting.Value
		if setting.Key == "core.write_audit_retention" && setting.Default != "2160h" {
			t.Fatal(setting.Default)
		}
		if setting.Key == "core.runtime_history" && (setting.Configured == nil || !*setting.Configured) {
			t.Fatal("history file was not reported as configured")
		}
	}
	if found["public_url"] != "https://core.example" || found["core.execution_concurrency"] != 8 || found["log.level"] != "warn" || found["log.format"] != "auto" || found["core.write_audit_retention"] != "24h" {
		t.Fatal(found)
	}
}
