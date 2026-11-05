package sessions

import (
	"encoding/json"
	"errors"
)

// TurnSettings are the official settings a root Turn runs with, under their
// official names. A Turn binds them when Core accepts the input that creates
// it, so a later Session update never reaches a Turn that already exists.
type TurnSettings struct {
	Model           string  `json:"model"`
	ReasoningEffort *string `json:"reasoning_effort"`
	ServiceTier     string  `json:"service_tier"`
	Review          string  `json:"review"`
}

// TurnSettingsOf projects a stored Session configuration into the settings a
// Turn accepted now binds. An absent review is none.
func TurnSettingsOf(configuration json.RawMessage) (TurnSettings, error) {
	var stored struct {
		Agent struct {
			Model     string `json:"model"`
			Reasoning struct {
				Effort *string `json:"effort"`
			} `json:"reasoning"`
			ServiceTier string `json:"service_tier"`
		} `json:"agent"`
		Review string `json:"review"`
	}
	if err := json.Unmarshal(configuration, &stored); err != nil {
		return TurnSettings{}, errors.New("invalid stored Session configuration")
	}
	settings := TurnSettings{Model: stored.Agent.Model, ReasoningEffort: stored.Agent.Reasoning.Effort, ServiceTier: stored.Agent.ServiceTier, Review: stored.Review}
	if settings.Review == "" {
		settings.Review = "none"
	}
	return settings, nil
}
