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
	public := config.PublicOrigin.String()
	facts := api.Installation{InstallationID: config.InstallationID, PublicURL: public, APIBaseURL: config.PublicOrigin.API(), LocalOnly: placement.LoopbackOrigin(public),
		Configuration: api.InstallationConfiguration{Settings: config.Settings()}}
	if sourceCommit.MatchString(buildRevision) {
		revision := buildRevision
		facts.SourceCommit = &revision
	}
	return facts
}
