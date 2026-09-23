package engine

import (
	"errors"
	"maps"
	"slices"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

var ErrInvalidInput = errors.New("invalid engine configuration")

// Profile records qualified public behavior, independently of Runtime advertisements.
type Profile struct {
	ProgrammaticToolCallingDisable             bool
	Placements                                 []string
	WebSearchControl, TextVerbosity, MCPBearer bool
	StructuredOutput                           bool
	ToolSearch                                 bool
	MessageImagePlacements                     []string
	ValidateConfiguration                      func(agent v1.Agent, environment *v1.Environment, hasDaemon bool) error
	ValidateTools                              func(environment *v1.Environment, hasDaemon bool, functions []proto.FunctionTool, mcp []proto.MCPHTTPServer) error
	ValidateFunctionResult                     func(placement string, result proto.FunctionResultPayload) error
	// WhitespaceOnlyText qualifies messages without an image or non-whitespace
	// text. Unqualified harnesses reject them at admission; Core never trims or
	// pads model input to fit a harness.
	WhitespaceOnlyText bool
}

func (p Profile) Accepts(placement string) bool {
	return slices.Contains(p.Placements, placement)
}

// Catalog is immutable after construction. Its zero value selects built-in profiles.
// NewCatalog with an empty map explicitly qualifies no engines.
type Catalog struct {
	profiles map[string]Profile
}

func NewCatalog(profiles map[string]Profile) Catalog {
	c := Catalog{profiles: make(map[string]Profile, len(profiles))}
	for kind, profile := range profiles {
		profile.Placements = slices.Clone(profile.Placements)
		profile.MessageImagePlacements = slices.Clone(profile.MessageImagePlacements)
		c.profiles[kind] = profile
	}
	return c
}

func (c Catalog) Lookup(kind string) (Profile, bool) {
	if c.profiles == nil {
		c = qualified
	}
	profile, ok := c.profiles[kind]
	profile.Placements = slices.Clone(profile.Placements)
	profile.MessageImagePlacements = slices.Clone(profile.MessageImagePlacements)
	return profile, ok
}

// Kinds returns a stable snapshot of harnesses qualified by this build.
func (c Catalog) Kinds() []string {
	if c.profiles == nil {
		c = qualified
	}
	kinds := slices.Collect(maps.Keys(c.profiles))
	slices.Sort(kinds)
	return kinds
}

var qualified = NewCatalog(map[string]Profile{
	"codex":      codexProfile(),
	"claude_sdk": claudeProfile(),
	"mcode":      mcodeProfile(),
})
