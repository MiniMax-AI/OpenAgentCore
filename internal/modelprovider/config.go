// Package modelprovider validates the frozen upstream connection supplied to a native Harness.
package modelprovider

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

type Protocol string

const (
	Anthropic       Protocol = "anthropic"
	Responses       Protocol = "responses"
	ChatCompletions Protocol = "chat_completions"
)

// Provider is the confidential, frozen upstream bundle supplied by Core.
// Engine selection and model identity remain separate execution inputs.
type Provider struct {
	Protocol        Protocol `json:"protocol"`
	BaseURL         string   `json:"base_url"`
	APIKey          string   `json:"api_key"`
	ContextWindow   int32    `json:"context_window,omitempty"`
	MaxOutputTokens int32    `json:"max_output_tokens,omitempty"`
}

var ErrConfiguration = errors.New("invalid model provider configuration")

func ParseProvider(raw any) (Provider, error) {
	var provider Provider
	body, err := json.Marshal(raw)
	if err != nil {
		return provider, ErrConfiguration
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&provider) != nil {
		return Provider{}, ErrConfiguration
	}
	if err := provider.Validate(); err != nil {
		return Provider{}, err
	}
	return provider, nil
}

// Valid is the single vocabulary of supported upstream protocol formats.
func (p Protocol) Valid() bool {
	switch p {
	case Anthropic, Responses, ChatCompletions:
		return true
	default:
		return false
	}
}

func (p Provider) Validate() error {
	if !p.Protocol.Valid() {
		return ErrConfiguration
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(p.BaseURL, "\x00\r\n") {
		return ErrConfiguration
	}
	// The scheme carries no admission decision here: an operator may point a
	// Harness at a plain-HTTP endpoint, including one on another host.
	if u.Scheme != "https" && u.Scheme != "http" {
		return ErrConfiguration
	}
	if strings.TrimSpace(p.APIKey) == "" || len(p.APIKey) > 16384 || strings.ContainsAny(p.APIKey, "\x00\r\n") {
		return ErrConfiguration
	}
	if p.ContextWindow < 0 || p.MaxOutputTokens < 0 || p.MaxOutputTokens > p.ContextWindow {
		return ErrConfiguration
	}
	return nil
}
