package v1

// CoreStartupConfiguration is a secret-free snapshot of the validated process
// configuration. It never describes a Session, Environment or live Runtime.
type CoreStartupConfiguration struct {
	Object                    string                             `json:"object" binding:"required" enums:"agents.core.startup_configuration"`
	SchemaVersion             int                                `json:"schema_version" binding:"required" enums:"1"`
	Supported                 CoreSupportedConfiguration         `json:"supported" binding:"required"`
	Configured                CoreConfiguredStartupConfiguration `json:"configured" binding:"required"`
	ConfigurationCapabilities *CoreConfigurationCapabilities     `json:"configuration_capabilities,omitempty"`
}

type CoreSupportedConfiguration struct {
	Harnesses               []string `json:"harnesses" binding:"required" enums:"claude_sdk,codex,mcode"`
	ManagedSandboxProviders []string `json:"managed_sandbox_providers" binding:"required" enums:"docker,microsandbox"`
}

type CoreConfiguredStartupConfiguration struct {
	DefaultHarness   string                                  `json:"default_harness" binding:"required" enums:"codex,claude_sdk,mcode"`
	EnabledHarnesses []string                                `json:"enabled_harnesses" binding:"required" enums:"claude_sdk,codex,mcode"`
	DaemonGateway    bool                                    `json:"daemon_gateway" binding:"required"`
	SelfHosted       bool                                    `json:"self_hosted" binding:"required"`
	ManagedSandbox   CoreManagedSandboxConfiguration         `json:"managed_sandbox" binding:"required"`
	ModelProviders   []CoreHarnessModelProviderConfiguration `json:"model_providers" binding:"required"`
}

type CoreManagedSandboxConfiguration struct {
	Enabled     bool    `json:"enabled" binding:"required"`
	Provider    *string `json:"provider" binding:"required" enums:"docker,microsandbox" extensions:"x-nullable"`
	Maintenance bool    `json:"maintenance" binding:"required"`
}

type CoreHarnessModelProviderConfiguration struct {
	Harness            string `json:"harness" binding:"required" enums:"codex,claude_sdk,mcode"`
	EndpointConfigured bool   `json:"endpoint_configured" binding:"required"`
}
