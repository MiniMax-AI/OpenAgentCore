package sandbox

// ConfigurationError is a fixed safe diagnostic, never SDK text or submitted data.
// Class describes the request outcome; it does not authorize replay.
type ConfigurationError struct {
	Class                ConfigurationErrorClass
	Code, Param, Message string
}
type ConfigurationErrorClass string

const (
	ConfigurationInvalid     ConfigurationErrorClass = "invalid"
	ConfigurationConflict    ConfigurationErrorClass = "conflict"
	ConfigurationUnconfirmed ConfigurationErrorClass = "unconfirmed"
)

func (e *ConfigurationError) Error() string { return e.Message }

var (
	ErrCredentialRejected       = &ConfigurationError{ConfigurationInvalid, "sandbox_credential_invalid", "credential", "The sandbox provider credential was rejected."}
	ErrCredentialOwnership      = &ConfigurationError{ConfigurationConflict, "sandbox_credential_ownership", "credential", "The credential cannot manage the retained deployment. Reset before changing accounts."}
	ErrConfigurationUnconfirmed = &ConfigurationError{ConfigurationUnconfirmed, "sandbox_verification_unconfirmed", "", "Sandbox provider verification could not be confirmed."}
	ErrConfigurationSelection   = &ConfigurationError{ConfigurationInvalid, "sandbox_configuration_invalid", "configuration", "Select a ready immutable provider configuration with matching resources."}
)
