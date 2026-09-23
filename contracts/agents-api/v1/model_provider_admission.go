package v1

import (
	"net/url"
	"strings"
)

const providerEnvironment = "openai_hosted"
const providerScheme = "https"

func ModelProviderEnvironmentSupported(environment string) bool {
	return environment == providerEnvironment
}

// ModelProviderConfigurationAdmission reports Core policy, independently of the
// adapter declaration and deployment enablement.
func ModelProviderConfigurationAdmission() CoreModelProviderAdmission {
	return CoreModelProviderAdmission{
		CredentialEnvironmentTypes: []string{providerEnvironment},
		BaseURL:                    CoreProviderEndpointRules{Schemes: []string{providerScheme}},
		TokenLimits:                CoreProviderTokenLimitRules{Minimum: 0, MaxOutputNotAboveContext: true},
	}
}

func validModelProviderBaseURL(base string) bool {
	u, err := url.Parse(base)
	return err == nil && u.Scheme == providerScheme && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(base, "\x00\r\n")
}
