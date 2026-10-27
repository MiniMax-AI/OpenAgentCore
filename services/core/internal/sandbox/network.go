package sandbox

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The deployment network rule is authored here once. The node installer and the
// TypeScript client consume its generated projection.
const (
	maxNetworkDomains = 100
	maxDomainLength   = 253
	// domainPattern accepts an exact host name: dot-separated RFC 1123 labels
	// whose last label starts with a letter, which also rejects IP literals.
	domainPattern = `^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$`
)

var domainRule = regexp.MustCompile(domainPattern)

// networkRule is the generated projection of the network format rule.
type networkRule struct {
	MaxDomains      int    `json:"max_domains"`
	MaxDomainLength int    `json:"max_domain_length"`
	DomainPattern   string `json:"domain_pattern"`
}

var networkContract = networkRule{maxNetworkDomains, maxDomainLength, domainPattern}

// Network is the deployment's egress cap: what its sandboxes may reach at most.
// Domains are exact host names whose resolved addresses are allowed, private
// ones included. Every other private, loopback and link-local address stays
// blocked, and the connection to Core is always allowed.
type Network struct {
	Public  bool     `json:"public" binding:"required"`
	Domains []string `json:"domains" binding:"required"`
}

// DefaultNetwork is the public internet only.
func DefaultNetwork() Network { return Network{Public: true, Domains: []string{}} }

// Canonical lowercases, deduplicates and sorts the domains. It is applied
// before persistence so equal caps have equal specification digests.
func (n Network) Canonical() Network {
	domains := make([]string, 0, len(n.Domains))
	for _, d := range n.Domains {
		domains = append(domains, strings.ToLower(strings.TrimSpace(d)))
	}
	slices.Sort(domains)
	n.Domains = slices.Compact(domains)
	return n
}

// ValidatePolicy checks the cap against the Provider's declaration.
func (n Network) ValidatePolicy(provider string, support NetworkSupport) error {
	if len(n.Domains) > maxNetworkDomains {
		return &ValidationError{Param: "network.domains", Message: fmt.Sprintf("%s: network.domains accepts at most %d domains", ErrInvalid, maxNetworkDomains)}
	}
	for _, d := range n.Domains {
		if len(d) > maxDomainLength || !domainRule.MatchString(d) {
			return &ValidationError{Param: "network.domains", Message: fmt.Sprintf("%s: %q is not an exact host name", ErrInvalid, d)}
		}
	}
	if !n.Public && !support.Restrict {
		return &ValidationError{Param: "network.public", Message: fmt.Sprintf("%s: %s cannot refuse public addresses", ErrInvalid, provider)}
	}
	if len(n.Domains) > 0 && !support.PrivateDomains {
		return &ValidationError{Param: "network.domains", Message: fmt.Sprintf("%s: %s cannot allow private domains", ErrInvalid, provider)}
	}
	return nil
}

// Validate checks an Egress a Provider receives: exact host names for
// PrivateDomains and non-empty host names or IP addresses for Endpoints.
func (e Egress) Validate() error {
	if len(e.PrivateDomains) > maxNetworkDomains {
		return ErrInvalid
	}
	for _, d := range e.PrivateDomains {
		if len(d) > maxDomainLength || !domainRule.MatchString(d) {
			return ErrInvalid
		}
	}
	for _, h := range e.Endpoints {
		if h == "" || len(h) > maxDomainLength || strings.ContainsAny(h, "/ \t\r\n") {
			return ErrInvalid
		}
	}
	return nil
}

// ValidDomain reports whether name is an exact host name under the deployment
// network rule.
func ValidDomain(name string) bool {
	return len(name) <= maxDomainLength && domainRule.MatchString(name)
}
