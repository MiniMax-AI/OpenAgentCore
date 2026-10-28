// Package agentnetwork validates the public Environment network field.
package agentnetwork

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

var errInvalid = errors.New("invalid hosted network policy")

type Policy struct {
	Access         string
	AllowedDomains []string
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
			return errInvalid
		}
		for _, domain := range p.AllowedDomains {
			if !hostname(domain) {
				return errInvalid
			}
		}
		return nil
	}
	return errInvalid
}

// HostPattern accepts an exact host name: dot-separated RFC 1123 labels whose
// last label starts with a letter, which also rejects IP literals. It is the one
// exact-host rule; the sandbox deployment cap shares it.
const HostPattern = `^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$`

var hostRule = regexp.MustCompile(HostPattern)

func hostname(value string) bool {
	return len(value) <= 253 && hostRule.MatchString(strings.ToLower(value))
}

// hosts is the case-folded, sorted allowlist. The caller must validate first.
func (p Policy) hosts() []string {
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
	hosts := template.hosts()
	for _, host := range p.hosts() {
		if _, ok := slices.BinarySearch(hosts, host); !ok {
			return false
		}
	}
	return true
}
