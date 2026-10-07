// Package processconfig is the process settings Core loads from its
// environment. Load reads every variable and every file it names exactly once,
// applies each default and validates the result; startup and `oac-core
// check-config` both call it. Errors name the variable and never include its
// value or the content of a file.
package processconfig

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/http/httpguts"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/databaseurl"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

const (
	defaultAddr                = "127.0.0.1:8091"
	defaultHarness             = "codex"
	defaultWriteAuditRetention = 90 * 24 * time.Hour
	// providerStateRoot is where Compose mounts the data volume's state/.
	providerStateRoot = "/state"
)

// Config is Core's process configuration.
type Config struct {
	// Addr is OAC_ADDR, the listener address.
	Addr string
	// PublicOrigin is OAC_PUBLIC_URL. Nil disables the Runtime gateway and
	// the execution Worker.
	PublicOrigin *deployment.PublicOrigin
	// DatabaseURL is OAC_DATABASE_URL with the password from
	// OAC_DATABASE_PASSWORD_FILE.
	DatabaseURL string
	// InstallationID comes from OAC_INSTALLATION_ID_FILE. Empty leaves the
	// sandbox deployment and node routes off.
	InstallationID string
	// AgentHostID and AgentHostCredentialHash are the deployment's agent host
	// from OAC_AGENT_HOST_IDENTITY_FILE, which the Runtime gateway requires;
	// Core registers it at startup. The hash is the one Runtime and Link
	// authentication compare.
	AgentHostID             string
	AgentHostCredentialHash string
	// CredentialKey seals stored credentials. Nil when
	// OAC_CREDENTIAL_KEY_FILE is unset.
	CredentialKey *credentialcrypto.Cipher
	// CoreKeys authenticates the Core key from OAC_CORE_KEY_DIGESTS_FILE.
	CoreKeys             *api.DeploymentAuthenticator
	ExecutionConcurrency int
	DefaultHarness       string
	// Harnesses is the sorted set of enabled Harnesses, the default included.
	Harnesses           []string
	WriteAuditRetention time.Duration
	OAuthTrustedOrigins []string
	RuntimeHistory      RuntimeHistory
	// ProviderPaths locate adapter helpers under OAC_PROVIDER_ROOT and their
	// private state under the Compose state mount.
	ProviderPaths sandbox.ProcessPaths
	// NativeInstallers is the native installer catalog directory under
	// OAC_PROVIDER_ROOT; empty when the root is unset.
	NativeInstallers string
	Log              log.Config
}

// RuntimeHistory is the file named by OAC_HISTORY_SETTINGS_FILE, with its
// defaults applied. Without the file, history stays in Core's database and
// nothing is exported.
type RuntimeHistory struct {
	// File is the path Core loaded, or empty.
	File string
	// Endpoint is an optional OTLP/HTTP metrics URL; Insecure allows http.
	Endpoint       string
	Insecure       bool
	Headers        map[string]string
	QueueCapacity  int
	Timeout        time.Duration
	SampleInterval time.Duration
}

// runtimeHistoryFile is the file's JSON schema.
type runtimeHistoryFile struct {
	Transport             string            `json:"transport,omitempty"`
	Endpoint              string            `json:"endpoint,omitempty"`
	Insecure              bool              `json:"insecure,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
	QueueCapacity         int               `json:"queue_capacity,omitempty"`
	TimeoutSeconds        int               `json:"timeout_seconds,omitempty"`
	SampleIntervalSeconds int               `json:"sample_interval_seconds,omitempty"`
}

// Load reads and validates the process environment. An unset or empty
// variable selects its default.
func Load() (Config, error) {
	var c Config
	var err error
	if c.Log, err = log.LoadConfig(); err != nil {
		return Config{}, err
	}
	c.Addr = cmp.Or(os.Getenv("OAC_ADDR"), defaultAddr)
	if value := os.Getenv("OAC_PUBLIC_URL"); value != "" {
		origin, err := deployment.NewPublicOrigin(value)
		if err != nil {
			return Config{}, configError("OAC_PUBLIC_URL must be a canonical http or https origin without path, credentials, query or fragment, such as https://core.example")
		}
		c.PublicOrigin = &origin
	}
	if c.DatabaseURL, err = databaseurl.FromEnvironment(); err != nil {
		return Config{}, err
	}
	if c.DatabaseURL == "" {
		return Config{}, configError("OAC_DATABASE_URL is required")
	}
	if c.InstallationID, err = installationID(); err != nil {
		return Config{}, err
	}
	if c.InstallationID != "" && c.PublicOrigin == nil {
		return Config{}, configError("OAC_INSTALLATION_ID_FILE requires OAC_PUBLIC_URL, the origin nodes and sandboxes use to reach Core")
	}
	if c.AgentHostID, c.AgentHostCredentialHash, err = agentHost(); err != nil {
		return Config{}, err
	}
	if c.AgentHostID != "" && c.PublicOrigin == nil {
		return Config{}, configError("OAC_AGENT_HOST_IDENTITY_FILE requires OAC_PUBLIC_URL, the origin of the Runtime gateway the agent host connects to")
	}
	if c.AgentHostID == "" && c.PublicOrigin != nil {
		return Config{}, configError("OAC_PUBLIC_URL requires OAC_AGENT_HOST_IDENTITY_FILE, the agent host its Runtime gateway serves")
	}
	if c.CredentialKey, err = credentialKey(); err != nil {
		return Config{}, err
	}
	if c.CoreKeys, err = coreKeys(); err != nil {
		return Config{}, err
	}
	if c.ExecutionConcurrency, err = executionConcurrency(); err != nil {
		return Config{}, err
	}
	c.DefaultHarness = cmp.Or(os.Getenv("OAC_DEFAULT_HARNESS"), defaultHarness)
	if !builtin.Contains(c.DefaultHarness) {
		return Config{}, configError("OAC_DEFAULT_HARNESS is not a known harness")
	}
	if c.Harnesses, err = harnesses(c.DefaultHarness); err != nil {
		return Config{}, err
	}
	if c.WriteAuditRetention, err = writeAuditRetention(); err != nil {
		return Config{}, err
	}
	if c.OAuthTrustedOrigins, err = oauthTrustedOrigins(); err != nil {
		return Config{}, err
	}
	if c.RuntimeHistory, err = runtimeHistory(); err != nil {
		return Config{}, err
	}
	c.ProviderPaths = sandbox.ProcessPaths{ArtifactRoot: os.Getenv("OAC_PROVIDER_ROOT"), StateRoot: providerStateRoot}
	if c.ProviderPaths.ArtifactRoot != "" {
		c.NativeInstallers = c.ProviderPaths.ArtifactRoot + "/native-installers"
	}
	return c, nil
}

// Settings projects the configuration that GET /core/v1/installation
// reports. Sensitive file settings report only whether they are configured.
func (c Config) Settings() []api.InstallationSetting {
	var public any
	if c.PublicOrigin != nil {
		public = c.PublicOrigin.String()
	}
	format := c.Log.Format
	if format == "" {
		format = "auto"
	}
	origins := c.OAuthTrustedOrigins
	if origins == nil {
		origins = []string{}
	}
	return []api.InstallationSetting{
		setting("public_url", public, nil, []string{"core", "web"}),
		setting("log.level", strings.ToLower(c.Log.Level.String()), "info", []string{"core", "web"}),
		setting("log.format", format, "auto", []string{"core", "web"}),
		setting("log.add_source", c.Log.AddSource, false, []string{"core", "web"}),
		setting("core.execution_concurrency", c.ExecutionConcurrency, execution.DefaultExecutionConcurrency, []string{"core"}),
		setting("core.harnesses", c.Harnesses, builtin.Kinds(), []string{"core"}),
		setting("core.default_harness", c.DefaultHarness, defaultHarness, []string{"core"}),
		setting("core.write_audit_retention", duration(c.WriteAuditRetention), duration(defaultWriteAuditRetention), []string{"core"}),
		setting("core.oauth_trusted_origins", origins, []string{}, []string{"core"}),
		sensitive("core.runtime_history", c.RuntimeHistory.File != "", []string{"core"}),
	}
}

// duration formats d as OAC_WRITE_AUDIT_RETENTION spells it: 2160h, not
// 2160h0m0s.
func duration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

func setting(key string, value, fallback any, restarts []string) api.InstallationSetting {
	return api.InstallationSetting{Key: key, Value: value, Default: fallback, Changeable: true, Sensitive: false, Restarts: restarts}
}

func sensitive(key string, configured bool, restarts []string) api.InstallationSetting {
	return api.InstallationSetting{Key: key, Configured: &configured, Changeable: true, Sensitive: true, Restarts: restarts}
}

func installationID() (string, error) {
	path := os.Getenv("OAC_INSTALLATION_ID_FILE")
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", configError("OAC_INSTALLATION_ID_FILE must name a readable file")
	}
	value := strings.TrimSpace(string(raw))
	if id, err := uuid.Parse(value); err != nil || id == uuid.Nil || id.String() != value {
		return "", configError("OAC_INSTALLATION_ID_FILE must contain a canonical UUID")
	}
	return value, nil
}

// agentHost reads the agent-host identity: its canonical Runtime ID and the
// digest of its credential, which the agent host presents verbatim.
func agentHost() (string, string, error) {
	path := os.Getenv("OAC_AGENT_HOST_IDENTITY_FILE")
	if path == "" {
		return "", "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", configError("OAC_AGENT_HOST_IDENTITY_FILE must name a readable file")
	}
	var identity struct {
		RuntimeID  string `json:"runtime_id"`
		Credential string `json:"credential"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&identity) != nil || decoder.Decode(new(any)) != io.EOF {
		return "", "", configError("OAC_AGENT_HOST_IDENTITY_FILE must hold one JSON object with runtime_id and credential")
	}
	if id, err := uuid.Parse(identity.RuntimeID); err != nil || id == uuid.Nil || id.String() != identity.RuntimeID || strings.TrimSpace(identity.Credential) == "" {
		return "", "", configError("OAC_AGENT_HOST_IDENTITY_FILE must hold a canonical UUID runtime_id and a credential")
	}
	return identity.RuntimeID, runtimedevice.HashCredential(identity.Credential), nil
}

func credentialKey() (*credentialcrypto.Cipher, error) {
	path := os.Getenv("OAC_CREDENTIAL_KEY_FILE")
	if path == "" {
		return nil, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, configError("cannot read OAC_CREDENTIAL_KEY_FILE")
	}
	key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(content)))
	if err != nil || len(key) != 32 {
		return nil, configError("OAC_CREDENTIAL_KEY_FILE must contain a base64-encoded random 32-byte key")
	}
	return credentialcrypto.New(key)
}

func coreKeys() (*api.DeploymentAuthenticator, error) {
	path := os.Getenv("OAC_CORE_KEY_DIGESTS_FILE")
	if path == "" {
		return nil, configError("OAC_CORE_KEY_DIGESTS_FILE is required; Core needs the Core key digest")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, configError("cannot read OAC_CORE_KEY_DIGESTS_FILE")
	}
	var digests []string
	if json.Unmarshal(raw, &digests) != nil {
		return nil, configError("OAC_CORE_KEY_DIGESTS_FILE must contain a JSON array of Core key SHA-256 digests")
	}
	keys, err := api.NewDeploymentAuthenticator(digests)
	if err != nil {
		return nil, configError("OAC_CORE_KEY_DIGESTS_FILE must contain a JSON array of Core key SHA-256 digests")
	}
	return keys, nil
}

func executionConcurrency() (int, error) {
	value := os.Getenv("OAC_EXECUTION_CONCURRENCY")
	if value == "" {
		return execution.DefaultExecutionConcurrency, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 1024 {
		return 0, configError("OAC_EXECUTION_CONCURRENCY must be an integer between 1 and 1024")
	}
	return limit, nil
}

// harnesses reads OAC_HARNESSES. Unset enables every Harness this build
// supports; a list supplements the default Harness.
func harnesses(defaultEngine string) ([]string, error) {
	kinds := []string{defaultEngine}
	if value := os.Getenv("OAC_HARNESSES"); value != "" {
		kinds = append(kinds, strings.Split(value, ",")...)
	} else {
		kinds = append(kinds, builtin.Kinds()...)
	}
	for i, kind := range kinds {
		kind = strings.TrimSpace(kind)
		if !builtin.Contains(kind) {
			return nil, configError("OAC_HARNESSES contains an unknown harness")
		}
		kinds[i] = kind
	}
	slices.Sort(kinds)
	return slices.Compact(kinds), nil
}

func writeAuditRetention() (time.Duration, error) {
	value := os.Getenv("OAC_WRITE_AUDIT_RETENTION")
	if value == "" {
		return defaultWriteAuditRetention, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < time.Hour {
		return 0, configError("OAC_WRITE_AUDIT_RETENTION must be a duration of at least 1h")
	}
	return duration, nil
}

func oauthTrustedOrigins() ([]string, error) {
	raw := os.Getenv("OAC_OAUTH_TRUSTED_ORIGINS")
	if raw == "" {
		return nil, nil
	}
	origins := strings.Split(raw, ",")
	for i, origin := range origins {
		origins[i] = strings.TrimSpace(origin)
	}
	if _, err := oauthrefresh.NewClient(origins); err != nil {
		return nil, configError("OAC_OAUTH_TRUSTED_ORIGINS is invalid")
	}
	return origins, nil
}

func runtimeHistory() (RuntimeHistory, error) {
	var file runtimeHistoryFile
	path := os.Getenv("OAC_HISTORY_SETTINGS_FILE")
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return RuntimeHistory{}, configError("OAC_HISTORY_SETTINGS_FILE: the file cannot be read")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&file) != nil || decoder.Decode(new(any)) != io.EOF {
			return RuntimeHistory{}, configError("OAC_HISTORY_SETTINGS_FILE: the file must hold one JSON object with known fields")
		}
	}
	if err := validateRuntimeHistory(file); err != nil {
		return RuntimeHistory{}, err
	}
	return RuntimeHistory{File: path, Endpoint: file.Endpoint, Insecure: file.Insecure, Headers: file.Headers,
		QueueCapacity:  cmp.Or(file.QueueCapacity, 256),
		Timeout:        time.Duration(cmp.Or(file.TimeoutSeconds, 2)) * time.Second,
		SampleInterval: time.Duration(cmp.Or(file.SampleIntervalSeconds, 30)) * time.Second}, nil
}

func validateRuntimeHistory(file runtimeHistoryFile) error {
	if file.QueueCapacity < 0 || file.QueueCapacity > 4096 {
		return configError("OAC_HISTORY_SETTINGS_FILE: queue_capacity must be from 0 to 4096")
	}
	if file.TimeoutSeconds < 0 || file.TimeoutSeconds > 30 {
		return configError("OAC_HISTORY_SETTINGS_FILE: timeout_seconds must be from 0 to 30")
	}
	if file.SampleIntervalSeconds != 0 && (file.SampleIntervalSeconds < 5 || file.SampleIntervalSeconds > 300) {
		return configError("OAC_HISTORY_SETTINGS_FILE: sample_interval_seconds must be from 5 to 300")
	}
	if file.Endpoint == "" {
		if file.Transport != "" || file.Insecure || len(file.Headers) != 0 {
			return configError("OAC_HISTORY_SETTINGS_FILE: transport, insecure and headers require an endpoint")
		}
		return nil
	}
	if file.Transport != "otlp_http" {
		return configError("OAC_HISTORY_SETTINGS_FILE: transport must be otlp_http")
	}
	endpoint, err := url.Parse(file.Endpoint)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.RawPath != "" || endpoint.Path == "" || endpoint.String() != file.Endpoint {
		return configError("OAC_HISTORY_SETTINGS_FILE: endpoint must be a canonical absolute OTLP metrics URL")
	}
	switch endpoint.Scheme {
	case "https":
		if file.Insecure {
			return configError("OAC_HISTORY_SETTINGS_FILE: insecure requires an http endpoint")
		}
	case "http":
		if !file.Insecure {
			return configError("OAC_HISTORY_SETTINGS_FILE: an http endpoint requires insecure=true")
		}
	default:
		return configError("OAC_HISTORY_SETTINGS_FILE: endpoint scheme must be https or explicit insecure http")
	}
	for key, value := range file.Headers {
		lower := strings.ToLower(key)
		if !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(value) || lower == "host" || lower == "content-length" || lower == "content-type" || lower == "content-encoding" {
			return configError("OAC_HISTORY_SETTINGS_FILE: headers contain an invalid or reserved entry")
		}
	}
	return nil
}

type configError string

func (e configError) Error() string { return string(e) }
