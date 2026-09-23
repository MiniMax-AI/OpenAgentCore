package v1

// CoreConfigurationCapabilities is the safe provider-configuration subset of
// this Core build's adapter declarations. It does not describe all Agent options
// or guarantee support across deployed Runtime versions.
type CoreConfigurationCapabilities struct {
	SchemaVersion       int                               `json:"schema_version" binding:"required" enums:"1"`
	Scope               string                            `json:"scope" binding:"required" enums:"core_build_provider_configuration"`
	RuntimeAvailability string                            `json:"runtime_availability" binding:"required" enums:"unknown"`
	Admission           CoreModelProviderAdmission        `json:"admission" binding:"required"`
	Harnesses           []CoreHarnessConfigurationSupport `json:"harnesses" binding:"required"`
}

type CoreHarnessConfigurationSupport struct {
	Harness   string                        `json:"harness" binding:"required"`
	Support   string                        `json:"support" binding:"required" enums:"supported,unknown"`
	Enabled   bool                          `json:"enabled" binding:"required"`
	Default   bool                          `json:"default" binding:"required"`
	Providers []CoreProviderProtocolSupport `json:"providers" binding:"required"`
}

type CoreProviderProtocolSupport struct {
	Protocol       string   `json:"protocol" binding:"required"`
	RequiredFields []string `json:"required_fields" binding:"required" enums:"protocol,base_url,api_key,context_window,max_output_tokens"`
	PositiveFields []string `json:"positive_fields" binding:"required" enums:"context_window,max_output_tokens"`
}

type CoreModelProviderAdmission struct {
	CredentialEnvironmentTypes []string                    `json:"credential_environment_types" binding:"required" enums:"openai_hosted"`
	BaseURL                    CoreProviderEndpointRules   `json:"base_url" binding:"required"`
	TokenLimits                CoreProviderTokenLimitRules `json:"token_limits" binding:"required"`
}

type CoreProviderEndpointRules struct {
	Schemes  []string `json:"schemes" binding:"required" enums:"https"`
	UserInfo bool     `json:"user_info" binding:"required"`
	Query    bool     `json:"query" binding:"required"`
	Fragment bool     `json:"fragment" binding:"required"`
}

type CoreProviderTokenLimitRules struct {
	Minimum                  int32 `json:"minimum" binding:"required"`
	MaxOutputNotAboveContext bool  `json:"max_output_not_above_context" binding:"required"`
}

func (c *CoreConfigurationCapabilities) Clone() *CoreConfigurationCapabilities {
	if c == nil {
		return nil
	}
	copy := *c
	copy.Admission.CredentialEnvironmentTypes = append([]string{}, c.Admission.CredentialEnvironmentTypes...)
	copy.Admission.BaseURL.Schemes = append([]string{}, c.Admission.BaseURL.Schemes...)
	copy.Harnesses = append([]CoreHarnessConfigurationSupport{}, c.Harnesses...)
	for i := range copy.Harnesses {
		copy.Harnesses[i].Providers = append([]CoreProviderProtocolSupport{}, c.Harnesses[i].Providers...)
		for j := range copy.Harnesses[i].Providers {
			provider := &copy.Harnesses[i].Providers[j]
			provider.RequiredFields = append([]string{}, provider.RequiredFields...)
			provider.PositiveFields = append([]string{}, provider.PositiveFields...)
		}
	}
	return &copy
}
