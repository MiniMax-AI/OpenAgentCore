package main

import (
	"errors"
	"io"
	"os"
	"regexp"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

var sourceCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

// installationFacts reports what GET /core/v1/installation serves: Core's own
// environment and build, plus the installer's settings snapshot. Core never
// acts on the snapshot; it only reports it.
func installationFacts(publicURL string) (api.Installation, error) {
	var facts api.Installation
	if id := os.Getenv("OAC_INSTALLATION_ID"); id != "" {
		facts.InstallationID = &id
	}
	if publicURL != "" {
		base := publicURL + "/v1"
		facts.PublicURL, facts.APIBaseURL, facts.LocalOnly = &publicURL, &base, store.LoopbackOrigin(publicURL)
	}
	if sourceCommit.MatchString(buildRevision) {
		revision := buildRevision
		facts.SourceCommit = &revision
	}
	path := os.Getenv("OAC_SETTINGS_FILE")
	if path == "" {
		return facts, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return facts, errors.New("cannot read OAC_SETTINGS_FILE")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 64<<10+1))
	if err != nil {
		return facts, errors.New("cannot read OAC_SETTINGS_FILE")
	}
	facts.Configuration, err = api.ParseInstallationConfiguration(raw)
	return facts, err
}
