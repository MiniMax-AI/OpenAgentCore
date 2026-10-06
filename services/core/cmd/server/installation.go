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
func installationFacts(publicURL string) (api.Installation, error) {
	var facts api.Installation
	id, err := processconfig.InstallationID()
	if err != nil {
		return facts, err
	}
	if id != "" {
		facts.InstallationID = &id
	}
	if publicURL != "" {
		base := publicURL + "/v1"
		facts.PublicURL, facts.APIBaseURL, facts.LocalOnly = &publicURL, &base, placement.LoopbackOrigin(publicURL)
	}
	if sourceCommit.MatchString(buildRevision) {
		revision := buildRevision
		facts.SourceCommit = &revision
	}
	settings, err := processconfig.Settings()
	if err != nil {
		return facts, err
	}
	facts.Configuration = &api.InstallationConfiguration{Settings: settings}
	return facts, nil
}
