package main

import (
	"regexp"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/processconfig"
)

var sourceCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

// installationFacts reports what GET /core/v1/installation serves: Core's own
// environment and build, plus the process settings it loaded.
func installationFacts(config processconfig.Config) api.Installation {
	facts := api.Installation{Configuration: api.InstallationConfiguration{Settings: config.Settings()}}
	if config.InstallationID != "" {
		facts.InstallationID = &config.InstallationID
	}
	if origin := config.PublicOrigin; origin != nil {
		public, base := origin.String(), origin.API()
		facts.PublicURL, facts.APIBaseURL, facts.LocalOnly = &public, &base, placement.LoopbackOrigin(public)
	}
	if sourceCommit.MatchString(buildRevision) {
		revision := buildRevision
		facts.SourceCommit = &revision
	}
	return facts
}
