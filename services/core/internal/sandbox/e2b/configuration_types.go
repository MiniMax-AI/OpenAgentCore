package e2b

// APIKey is internal configuration. HTTP requests use a write-only DTO.
// DeploymentBuild is set only by Core after it validates the candidate.
type DeploymentConfiguration struct {
	metadata           *buildMetadata
	CredentialSupplied bool             `json:"-"`
	APIKey             string           `json:"-"`
	APIURL             string           `json:"api_url,omitempty"`
	Domain             string           `json:"domain,omitempty"`
	Template           string           `json:"template"`
	TemplateBuild      *DeploymentBuild `json:"-"`
}

// DeploymentBuild is the fixed build as read by the validation that
// admitted a selection. RootDiskMiB is nil when E2B does not report it.
type DeploymentBuild struct {
	Status          string
	CPUs, MemoryMiB int32
	RootDiskMiB     *int32
}

func (c *DeploymentConfiguration) HasCredential() bool      { return c != nil && c.APIKey != "" }
func (c *DeploymentConfiguration) ReplacesCredential() bool { return c != nil && c.CredentialSupplied }
func (c *DeploymentConfiguration) String() string           { return "E2B deployment configuration (private)" }

// MarshalJSON blocks accidental serialization; ConfigurationAdapter.Encode owns the public projection.
func (*DeploymentConfiguration) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
