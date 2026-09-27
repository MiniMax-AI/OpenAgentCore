package main

import (
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/oauthrefresh"
)

func oauthRefreshClient() (*oauthrefresh.Client, error) {
	var origins []string
	if raw := os.Getenv("OAC_OAUTH_TRUSTED_ORIGINS"); raw != "" {
		for _, origin := range strings.Split(raw, ",") {
			origins = append(origins, strings.TrimSpace(origin))
		}
	}
	return oauthrefresh.NewClient(origins)
}
