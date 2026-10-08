// Package placement decides whether the sandbox deployment admits new hosted
// work and which node receives it. Its rules are pure: they read the
// deployment and node facts the caller loads under the deployment lock, the
// provider declarations and the installation public URL, and do no I/O. The
// caller applies what they decide in the same transaction.
package placement

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

var (
	// ErrResetAdmission rejects new hosted work while a sandbox reset pauses
	// admission.
	ErrResetAdmission = errors.New("hosted admission is paused for a sandbox reset")
	// ErrAdmissionClosed rejects new hosted work that the deployment cannot
	// admit: without a valid specification, or for another installation.
	ErrAdmissionClosed = errors.New("environment is no longer available")
	// ErrPublicURLUnreachable rejects hosted selection and admission while the
	// installation public URL is loopback or not https.
	ErrPublicURLUnreachable = errors.New("This sandbox provider needs a reachable HTTPS public URL before they can connect to Core.")
	// ErrNodesPreparing rejects placement while no node serves the target
	// generation and at least one is preparing it.
	ErrNodesPreparing = errors.New("sandbox nodes are preparing the target generation")
	// ErrNodeUnavailable reports a sandbox node that is offline, unready or
	// full, no node at all, or a deployment without a provider.
	ErrNodeUnavailable = errors.New("sandbox node unavailable")
	// ErrPublicURLRequired rejects rules without the installation public URL.
	ErrPublicURLRequired = errors.New("placement rules require the installation public URL")
)

// Declarations are the provider declarations placement reads.
// *providers.Registry satisfies it.
type Declarations interface {
	// ValidateSpecification rejects a specification the provider cannot run.
	ValidateSpecification(provider string, spec sandbox.DeploymentSpec) error
}

// Rules decides admission and placement for one installation. Its
// declarations and public URL never change after NewRules.
type Rules struct {
	declarations Declarations
	publicURL    string
}

// NewRules returns the rules for the provider declarations and the
// installation public URL.
func NewRules(declarations Declarations, publicURL string) (*Rules, error) {
	if declarations == nil {
		return nil, errors.New("placement rules require provider declarations")
	}
	if publicURL == "" {
		return nil, ErrPublicURLRequired
	}
	return &Rules{declarations: declarations, publicURL: publicURL}, nil
}

// PublicURL returns the installation public URL, OAC_PUBLIC_URL. Core
// derives every address it gives nodes, sandboxes and administrators from it.
func (r *Rules) PublicURL() string { return r.publicURL }

// Deployment is the deployment as placement reads it, loaded under the
// deployment lock.
type Deployment struct {
	// InstallationID is empty until the execution owner claims the
	// installation at startup.
	InstallationID string
	Provider, Mode string
	Generation     uint64
	// Resetting reports a sandbox reset in progress.
	Resetting     bool
	Specification json.RawMessage
}

// Node is one node of the installation with its presence and usage.
type Node struct {
	ID                   string
	Online, ServingReady bool
	// ReadyGeneration is nil until the node serves a generation.
	ReadyGeneration        *uint64
	TargetState            string
	Active, Retained       int64
	MaxActive, MaxRetained int
	CoreURL                string
}

// Placement is the node and generation a new Session's Environment reserves.
type Placement struct {
	NodeID     string
	Generation uint64
}

// Reserved is the node placement a Session reserved for its Environment.
type Reserved struct {
	NodeID     string
	Generation uint64
	Released   bool
	// Available reports that the node is connected, fresh and ready for the
	// reserved generation.
	Available bool
}

// Restore is what restoring suspended compute on its node reads: the node,
// nil when the installation no longer has it, and whether the node is ready
// for the allocation's generation.
type Restore struct {
	Node *Node
	// Generation is zero when the allocation has none.
	Generation      uint64
	GenerationReady bool
}

// CheckPublicOrigin rejects hosted sandboxes while the installation public URL
// is loopback or not https. Every hosted sandbox runs outside Core's network
// namespace and dials the sandbox Link: a loopback host names the sandbox's
// own namespace, and an http origin elsewhere has no Link.
func (r *Rules) CheckPublicOrigin() error {
	if !strings.HasPrefix(r.publicURL, "https://") || LoopbackOrigin(r.publicURL) {
		return ErrPublicURLUnreachable
	}
	return nil
}

// CheckAdmission admits new hosted work on the deployment. installation is
// the canonical installation the work was provisioned for, or empty for a new
// Session. Only a claimed deployment with a provider admits hosted work.
func (r *Rules) CheckAdmission(d Deployment, installation string) error {
	if d.InstallationID == "" || d.Provider == "" {
		return ErrNodeUnavailable
	}
	if d.Resetting {
		return ErrResetAdmission
	}
	var spec sandbox.DeploymentSpec
	if json.Unmarshal(d.Specification, &spec) != nil || r.declarations.ValidateSpecification(d.Provider, spec) != nil {
		return fmt.Errorf("%w: sandbox creation requires a deployment specification", ErrAdmissionClosed)
	}
	if installation != "" && installation != d.InstallationID {
		return fmt.Errorf("%w: sandbox installation does not match deployment", ErrAdmissionClosed)
	}
	return nil
}

// DecidePlacement chooses the node a new Session's hosted Environment
// reserves, or nil in direct mode, which places no node. It prefers the
// highest ready generation, then the fewest active sandboxes. It places only
// on nodes enrolled with the public URL; restores still reach the others.
func (r *Rules) DecidePlacement(d Deployment, nodes []Node) (*Placement, error) {
	if d.Resetting {
		return nil, ErrResetAdmission
	}
	if d.Provider == "" {
		return nil, ErrNodeUnavailable
	}
	// A changed installation address cannot admit hosted sandboxes. Existing
	// owned resources remain available for cleanup.
	if err := r.CheckPublicOrigin(); err != nil {
		return nil, err
	}
	if d.Mode == "direct" {
		return nil, nil
	}
	var chosen *Node
	preparing := false
	for i := range nodes {
		n := &nodes[i]
		free := n.Active < int64(n.MaxActive) && n.Retained < int64(n.MaxRetained)
		reachable := n.CoreURL == r.publicURL
		if n.Online && n.TargetState == "preparing" && free && reachable {
			preparing = true
		}
		if !n.Online || !n.ServingReady || n.ReadyGeneration == nil || !free || !reachable {
			continue
		}
		if chosen == nil || *n.ReadyGeneration > *chosen.ReadyGeneration || *n.ReadyGeneration == *chosen.ReadyGeneration && n.Active < chosen.Active {
			chosen = n
		}
	}
	if chosen == nil {
		if preparing {
			return nil, ErrNodesPreparing
		}
		return nil, ErrNodeUnavailable
	}
	return &Placement{NodeID: chosen.ID, Generation: *chosen.ReadyGeneration}, nil
}

// CheckReserved admits an allocation on the node placement its Session
// reserved: the placement must be unreleased and its node available.
func CheckReserved(reserved Reserved) error {
	if reserved.Released || !reserved.Available {
		return ErrNodeUnavailable
	}
	return nil
}

// CheckRestore admits restoring suspended compute on its original node: the
// node must be online, ready for the allocation's generation and below its
// active capacity. Restore never selects another node.
func CheckRestore(restore Restore) error {
	n := restore.Node
	if n == nil || restore.Generation == 0 || !n.Online || !restore.GenerationReady || n.Active >= int64(n.MaxActive) {
		return ErrNodeUnavailable
	}
	return nil
}

// LoopbackOrigin reports whether a validated origin names a loopback host,
// which nothing outside the Core host can reach.
func LoopbackOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return ip.IsLoopback()
	}
	return u.Hostname() == "localhost"
}
