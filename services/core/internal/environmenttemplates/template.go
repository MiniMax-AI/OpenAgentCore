package environmenttemplates

import (
	"time"
	"unicode/utf8"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentnetwork"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
)

// Template is a saved Template's safe metadata: it holds no confidential
// setup, file contents or archives, so reading it needs no credential key.
type Template struct {
	ID                    string
	Name                  *string
	NetworkAccess         string
	AllowedDomains        []string
	Packages              v1.EnvironmentPackages
	Files                 []environmentconfig.InitialFileMetadata
	Skills                []environmentconfig.SkillMetadata
	Plugins               []agentplugin.Metadata
	CapabilityDirectories []string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Resolved is a Template with its decrypted configuration, read in one snapshot
// for Session creation. Skill references keep the selectors the Template saved.
type Resolved struct {
	Template Template
	Setup    environmentconfig.Setup
	Files    []environmentconfig.InitialFile
}

// Input is Template configuration as a create or update request states it.
// Create saves every field. Update replaces exactly the fields whose Set flag
// is true, each atomically, and keeps the others.
type Input struct {
	Name           *string
	SetName        bool
	NetworkAccess  string
	AllowedDomains []string
	SetNetwork     bool
	Files          []environmentconfig.InitialFile
	SetFiles       bool
	Setup          environmentconfig.Setup
	SetEnv         bool
	SetCommands    bool
	SetPackages    bool
	SetSkills      bool
	SetPlugins     bool
	SetDirectories bool
}

// MaxNameLength is the longest Template name, in characters.
const MaxNameLength = 256

// ValidateName accepts no name or a valid UTF-8 name of 1 to MaxNameLength
// characters.
func ValidateName(name *string) error {
	if name != nil && (!utf8.ValidString(*name) || utf8.RuneCountInString(*name) < 1 || utf8.RuneCountInString(*name) > MaxNameLength) {
		return ErrInvalidInput
	}
	return nil
}

// Validate reports whether the input is valid Template configuration. The
// network policy is checked only when the input sets one.
func (in Input) Validate() error {
	if ValidateName(in.Name) != nil || in.Setup.Validate() != nil || environmentconfig.ValidateInitialFiles(in.Files) != nil {
		return ErrInvalidInput
	}
	if in.SetNetwork && (agentnetwork.Policy{Access: in.NetworkAccess, AllowedDomains: in.AllowedDomains}).Validate() != nil {
		return ErrInvalidInput
	}
	return nil
}

// withDefaultNetwork gives a new Template without a network policy the pinned
// default: network access enabled.
func (in Input) withDefaultNetwork() Input {
	if !in.SetNetwork {
		in.NetworkAccess, in.AllowedDomains, in.SetNetwork = "enabled", nil, true
	}
	return in
}

// MaxListLimit is the largest page a list returns.
const MaxListLimit = 100

// ListQuery selects one page of a tenant's Templates in creation order, with ID
// tie-breaking. After is the previous page's last Template ID, or empty.
type ListQuery struct {
	After     string
	Limit     int
	Ascending bool
}

// Validate accepts a Limit from 1 to MaxListLimit.
func (q ListQuery) Validate() error {
	if q.Limit < 1 || q.Limit > MaxListLimit {
		return ErrInvalidInput
	}
	return nil
}

// Page is one page of Templates.
type Page struct {
	Templates []Template
	HasMore   bool
}
