// Package agentnetwork validates the hosted execution policy without selecting
// a harness, Provider, native proxy or initialization mechanism.
package agentnetwork

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
)

var ErrInvalid = errors.New("invalid hosted network policy")

type Policy struct {
	Access         string   `json:"access"`
	AllowedDomains []string `json:"allowed_domains"`
}

// Validate accepts the qualified exact ASCII hostname form. It does not rewrite
// public field spelling, order or duplicates, or infer defaults from missing access.
func (p Policy) Validate() error {
	switch p.Access {
	case "enabled", "disabled":
		if len(p.AllowedDomains) == 0 {
			return nil
		}
	case "restricted":
		if len(p.AllowedDomains) < 1 || len(p.AllowedDomains) > 100 {
			return ErrInvalid
		}
		for _, domain := range p.AllowedDomains {
			if !hostname(domain) {
				return ErrInvalid
			}
		}
		return nil
	}
	return ErrInvalid
}

func hostname(value string) bool {
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

// Hosts is the deterministic native allowlist. The caller must validate first.
// DNS host identity is case-insensitive; public metadata remains untouched.
func (p Policy) Hosts() []string {
	hosts := make([]string, len(p.AllowedDomains))
	for i, host := range p.AllowedDomains {
		hosts[i] = strings.ToLower(host)
	}
	slices.Sort(hosts)
	return slices.Compact(hosts)
}

// Narrows reports whether this policy is a valid subset of the template policy.
func (p Policy) Narrows(template Policy) bool {
	if p.Validate() != nil || template.Validate() != nil {
		return false
	}
	if template.Access == "enabled" || p.Access == "disabled" {
		return true
	}
	if template.Access != "restricted" || p.Access != "restricted" {
		return false
	}
	hosts := template.Hosts()
	for _, host := range p.Hosts() {
		if _, ok := slices.BinarySearch(hosts, host); !ok {
			return false
		}
	}
	return true
}

// Equal compares validated effective authority, independent of public list order.
func (p Policy) Equal(other Policy) bool {
	return p.Access == other.Access && p.Validate() == nil && other.Validate() == nil && slices.Equal(p.Hosts(), other.Hosts())
}
