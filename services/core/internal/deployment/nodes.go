package deployment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

// ListNodes returns the installation's nodes.
func (s *Service) ListNodes(ctx context.Context) ([]Node, error) {
	records, err := s.reader.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(records))
	for _, n := range records {
		value, err := s.node(n)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func (s *Service) node(n NodeRecord) (Node, error) {
	retained, err := s.registry.RetainedLimit(n.Provider, n.MaxActive, n.MaxRetained)
	if err != nil {
		return Node{}, err
	}
	health := n.Health
	health.Host = nil
	health.ProviderReady = n.Online && n.ServingReady
	return Node{Rollout: nodeRollout(n), NodeHealth: health, Running: n.Running, Snapshots: n.Snapshots, ID: n.ID, Name: n.Name, CoreURL: n.CoreURL, EnrollmentID: n.EnrollmentID, Provider: n.Provider, Online: n.Online, LastSeenAt: n.LastSeenAt, MaxActive: n.MaxActive, MaxRetained: retained, Active: n.Active, Reserved: n.Reserved, Retained: n.Retained, CleanupPending: n.CleanupPending, CreatedAt: n.CreatedAt}, nil
}

// NodeDetail returns one node with its last host observation and its host
// history over the named window: 1h, 6h or 24h.
func (s *Service) NodeDetail(ctx context.Context, id, window string) (NodeDetail, error) {
	nodeID, err := parseID(id)
	if err != nil {
		return NodeDetail{}, err
	}
	if window != "1h" && window != "6h" && window != "24h" {
		return NodeDetail{}, ErrInvalidInput
	}
	span, err := coremetrics.Window(time.Now(), window)
	if err != nil {
		return NodeDetail{}, ErrInvalidInput
	}
	record, samples, err := s.reader.NodeHistory(ctx, nodeID, span)
	if err != nil {
		return NodeDetail{}, err
	}
	node, err := s.node(record)
	if err != nil {
		return NodeDetail{}, err
	}
	result := NodeDetail{Node: node, History: HostHistory{ResolutionSeconds: span.ResolutionSeconds, Points: historyPoints(span, samples)}}
	if record.Health.Host != nil {
		result.Host = *record.Health.Host
	}
	return result, nil
}

// withManager runs apply over an initialized deployment.
func (s *Service) withManager(ctx context.Context, apply func(NodeTx, Record) error) error {
	return s.storage.WithNodes(ctx, func(tx NodeTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if !initialized(d) {
			return placement.ErrNodeUnavailable
		}
		return apply(tx, d)
	})
}

// UpdateNode renames a node and changes its capacity.
func (s *Service) UpdateNode(ctx context.Context, id string, update NodeUpdate) error {
	// The retained limit depends on the provider, so the transaction checks it.
	if err := validateNode(update.Name, update.MaxActive, update.MaxActive); err != nil {
		return err
	}
	nodeID, err := parseID(id)
	if err != nil {
		return err
	}
	return s.withManager(ctx, func(tx NodeTx, d Record) error {
		retained, err := s.registry.RetainedLimit(d.Provider, update.MaxActive, update.MaxRetained)
		if err != nil {
			return err
		}
		if err := validateNode(update.Name, update.MaxActive, retained); err != nil {
			return err
		}
		n, err := tx.LoadNode(nodeID)
		if err != nil {
			return err
		}
		if n.InstallationID != d.InstallationID {
			return ErrNotFound
		}
		return tx.UpdateNode(nodeID, NodeLimits{Name: update.Name, MaxActive: update.MaxActive, MaxRetained: retained})
	})
}

// RemoveNode removes a node that retains no resources and is not the local node.
func (s *Service) RemoveNode(ctx context.Context, id string) error {
	nodeID, err := parseID(id)
	if err != nil {
		return err
	}
	return s.withManager(ctx, func(tx NodeTx, d Record) error {
		nodes, err := tx.ListNodes()
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if n.ID != nodeID {
				continue
			}
			if n.Retained != 0 || n.CleanupPending != 0 {
				return ErrNodeInUse
			}
			if d.LocalNodeID == nodeID {
				return ErrLocalNodeConfigured
			}
			return tx.RemoveNode(nodeID)
		}
		return ErrNotFound
	})
}

// CreateEnrollment issues a one-use enrollment token. Its ID is a public,
// non-secret handle: the node the token registers reports it as enrollment_id.
func (s *Service) CreateEnrollment(ctx context.Context, capacity Capacity) (EnrollmentToken, error) {
	// The retained limit depends on the provider, so the transaction checks it.
	if err := validateNode("enrollment", capacity.MaxActive, capacity.MaxActive); err != nil {
		return EnrollmentToken{}, err
	}
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return EnrollmentToken{}, err
	}
	result := EnrollmentToken{Token: hex.EncodeToString(bytes[:]), ID: uuid.NewString()}
	err := s.withManager(ctx, func(tx NodeTx, d Record) error {
		retained, err := s.registry.RetainedLimit(d.Provider, capacity.MaxActive, capacity.MaxRetained)
		if err != nil {
			return err
		}
		if err := validateNode("enrollment", capacity.MaxActive, retained); err != nil {
			return err
		}
		if d.Reset != nil {
			return ErrResetInProgress
		}
		if d.Mode != "nodes" || d.AdmissionPaused {
			return ErrConflict
		}
		if _, err := s.specification(d); err != nil {
			return ErrConflict
		}
		result.ExpiresAt, err = tx.CreateEnrollment(NewEnrollment{ID: result.ID, TokenDigest: tokenDigest(result.Token), InstallationID: d.InstallationID, MaxActive: capacity.MaxActive, MaxRetained: retained})
		return err
	})
	if err != nil {
		return EnrollmentToken{}, err
	}
	return result, nil
}

// Enroll registers a node with a one-use enrollment token.
func (s *Service) Enroll(ctx context.Context, token string, input Enrollment) (NodeIdentity, error) {
	nodeID, err := parseID(input.NodeID)
	if err != nil || !validNodeCredential(input.Credential) || !validDigest(input.BackendFingerprint) {
		return NodeIdentity{}, ErrInvalidInput
	}
	if err := validateNode(input.Name, 1, 1); err != nil {
		return NodeIdentity{}, err
	}
	var result NodeIdentity
	err = s.storage.WithNodes(ctx, func(tx NodeTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		receipt, err := tx.LoadEnrollment(tokenDigest(token))
		if errors.Is(err, ErrNotFound) {
			return ErrNodeCredential
		}
		if err != nil {
			return placement.ErrNodeUnavailable
		}
		if receipt.Consumed || !receipt.ExpiresAt.After(time.Now()) {
			return ErrNodeCredential
		}
		if d.InstallationID != "" && receipt.InstallationID != d.InstallationID {
			return ErrNodeCredential
		}
		if !initialized(d) {
			return placement.ErrNodeUnavailable
		}
		if d.Reset != nil {
			return ErrResetInProgress
		}
		if d.Mode != "nodes" || d.AdmissionPaused || input.Provider != d.Provider {
			return ErrInvalidInput
		}
		spec, err := s.specification(d)
		if err != nil || input.DeploymentGeneration != d.Generation || input.SpecificationDigest != spec.Digest(d.Provider) {
			return ErrSpecificationMismatch
		}
		// The node must use the address Core advertises now. It read that address
		// from its configuration, but the public URL may have changed since, or an
		// operator may have registered by hand with another origin.
		if input.CoreURL != s.rules.PublicURL() {
			return ErrNodeAddressMismatch
		}
		retained, err := s.registry.RetainedLimit(d.Provider, receipt.MaxActive, receipt.MaxRetained)
		if err != nil {
			return err
		}
		n, err := tx.InsertNode(NewNode{ID: nodeID, InstallationID: d.InstallationID, Name: input.Name, BackendFingerprint: input.BackendFingerprint, CredentialDigest: tokenDigest(input.Credential), MaxActive: receipt.MaxActive, MaxRetained: retained, SpecificationDigest: input.SpecificationDigest, DeploymentGeneration: input.DeploymentGeneration, CoreURL: input.CoreURL, EnrollmentID: receipt.ID})
		if err != nil {
			return err
		}
		consumed, err := tx.ConsumeEnrollment(tokenDigest(token), nodeID)
		if err != nil {
			return err
		}
		if !consumed {
			return ErrNodeCredential
		}
		result, err = s.identity(n, d.Provider)
		return err
	})
	if err != nil {
		return NodeIdentity{}, err
	}
	return result, nil
}

func (s *Service) identity(n StoredNode, provider string) (NodeIdentity, error) {
	retained, err := s.registry.RetainedLimit(provider, n.MaxActive, n.MaxRetained)
	if err != nil {
		return NodeIdentity{}, err
	}
	return NodeIdentity{SpecificationDigest: n.SpecificationDigest, DeploymentGeneration: n.DeploymentGeneration, NodeID: n.ID, InstallationID: n.InstallationID, Provider: provider, BackendFingerprint: n.BackendFingerprint, MaxActive: n.MaxActive, MaxRetained: retained}, nil
}

// AuthenticateNode checks a node credential and returns the node's identity.
func (s *Service) AuthenticateNode(ctx context.Context, nodeID, credential string) (NodeIdentity, error) {
	id, err := parseID(nodeID)
	if err != nil {
		return NodeIdentity{}, ErrNodeCredential
	}
	var result NodeIdentity
	err = s.reader.ReadNodes(ctx, func(tx NodeReads) error {
		n, err := tx.LoadNode(id)
		if errors.Is(err, ErrNotFound) {
			return ErrNodeCredential
		}
		if err != nil {
			return placement.ErrNodeUnavailable
		}
		if n.CredentialDigest != tokenDigest(credential) {
			return ErrNodeCredential
		}
		d, err := tx.LoadDeployment()
		if err != nil {
			return placement.ErrNodeUnavailable
		}
		if n.InstallationID != d.InstallationID || d.Mode != "nodes" {
			return ErrNodeCredential
		}
		if err := s.checkEnrollmentIdentity(tx, d, n); err != nil {
			return err
		}
		// Reset retires nodes before another backend lineage can be selected.
		// Enrollment generation and digest remain immutable identity history; the
		// current target and per-generation readiness do not replace that history.
		result, err = s.identity(n, d.Provider)
		return err
	})
	if err != nil {
		return NodeIdentity{}, err
	}
	return result, nil
}

// checkEnrollmentIdentity keeps the enrollment identity valid after collection
// of its old specification. Whenever that specification is still
// authoritative, its exact digest must agree.
func (s *Service) checkEnrollmentIdentity(tx NodeReads, d Record, n StoredNode) error {
	if n.DeploymentGeneration == 0 || n.DeploymentGeneration > d.Generation || !validDigest(n.SpecificationDigest) {
		return ErrSpecificationMismatch
	}
	spec, err := s.generationSpecification(tx, d, n.DeploymentGeneration)
	if errors.Is(err, ErrNotFound) && n.DeploymentGeneration < d.Generation {
		return nil
	}
	if err != nil || spec.Digest(d.Provider) != n.SpecificationDigest {
		return ErrSpecificationMismatch
	}
	return nil
}

// generationSpecification returns a generation nodes may prepare when its
// provider still accepts it.
func (s *Service) generationSpecification(tx NodeReads, d Record, generation uint64) (sandbox.DeploymentSpec, error) {
	var spec sandbox.DeploymentSpec
	if !validGeneration(generation) {
		return spec, ErrInvalidInput
	}
	row, err := tx.LoadGenerationSpecification(generation)
	if err != nil {
		return spec, err
	}
	if row.Provider != d.Provider || json.Unmarshal(row.Specification, &spec) != nil || s.registry.ValidateSpecification(d.Provider, spec) != nil {
		return spec, ErrSpecificationMismatch
	}
	return spec, nil
}

// NodeStatus reports the authenticated node's Core-owned presence.
func (s *Service) NodeStatus(ctx context.Context, nodeID, credential string) (NodeStatus, error) {
	identity, err := s.AuthenticateNode(ctx, nodeID, credential)
	if err != nil {
		return NodeStatus{}, err
	}
	nodes, err := s.reader.Nodes(ctx)
	if err != nil {
		return NodeStatus{}, err
	}
	for _, n := range nodes {
		if n.ID == identity.NodeID {
			return NodeStatus{NodeIdentity: identity, Connected: n.Online, ProviderReady: n.Online && n.ServingReady}, nil
		}
	}
	return NodeStatus{}, ErrNodeCredential
}

// NodeConfiguration is the read-only bootstrap of an enrollment token or node
// credential. It never consumes tokens or reveals cloud credentials, and it
// authenticates the credential before reporting any deployment state. The
// deployment lock keeps authentication and the returned generation consistent.
//
// A nonzero generation recovers that exact generation, restricted to this
// node's current target, serving pin and unreleased ownership. It is never a
// general history read.
func (s *Service) NodeConfiguration(ctx context.Context, nodeID, token string, generation uint64) (NodeConfiguration, error) {
	var result NodeConfiguration
	if generation > math.MaxInt64 {
		return result, ErrInvalidInput
	}
	err := s.storage.WithNodes(ctx, func(tx NodeTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		var node *StoredNode
		var installation string
		var active, retained int
		if nodeID == "" {
			r, err := tx.LoadEnrollment(tokenDigest(token))
			if err != nil || r.Consumed || !r.ExpiresAt.After(time.Now()) {
				return ErrNodeCredential
			}
			installation, active, retained = r.InstallationID, r.MaxActive, r.MaxRetained
		} else {
			id, err := parseID(nodeID)
			if err != nil {
				return ErrNodeCredential
			}
			n, err := tx.LoadNode(id)
			if err != nil || n.CredentialDigest != tokenDigest(token) {
				return ErrNodeCredential
			}
			node, installation, active, retained = &n, n.InstallationID, n.MaxActive, n.MaxRetained
		}
		// A claimed installation rejects foreign credentials identically before
		// and after initialization.
		if d.InstallationID != "" && installation != d.InstallationID {
			return ErrNodeCredential
		}
		if !initialized(d) {
			return placement.ErrNodeUnavailable
		}
		if node == nil && d.Reset != nil {
			return ErrResetInProgress
		}
		if d.Mode != "nodes" {
			return ErrConflict
		}
		if node != nil {
			if err := s.checkEnrollmentIdentity(tx, d, *node); err != nil {
				return err
			}
		}
		selected := d.Generation
		if generation != 0 {
			if node == nil {
				return ErrNodeCredential
			}
			selected = generation
			kept, err := tx.GenerationKept(node.ID, generation)
			if err != nil {
				return err
			}
			if !kept {
				return ErrSpecificationMismatch
			}
		}
		spec, err := s.generationSpecification(tx, d, selected)
		if err != nil {
			return err
		}
		if node == nil && d.AdmissionPaused {
			return ErrConflict
		}
		limit, err := s.registry.RetainedLimit(d.Provider, active, retained)
		if err != nil {
			return err
		}
		result = NodeConfiguration{MaxActive: active, MaxRetained: limit, InstallationID: d.InstallationID, Provider: d.Provider, CoreURL: s.rules.PublicURL(), Generation: selected, Specification: spec, SpecificationDigest: spec.Digest(d.Provider)}
		return nil
	})
	if err != nil {
		return NodeConfiguration{}, err
	}
	return result, nil
}

// connection returns the node of a current connection of this owner epoch.
func (s *Service) connection(tx NodeReads, d Record, nodeID, connectionID string, epoch uint64) (StoredNode, error) {
	id, err := parseID(nodeID)
	if err != nil {
		return StoredNode{}, ErrNodeCredential
	}
	n, err := tx.LoadNode(id)
	if errors.Is(err, ErrNotFound) {
		return StoredNode{}, ErrNodeCredential
	}
	if err != nil {
		return StoredNode{}, err
	}
	if n.ConnectionID != connectionID || n.ConnectedEpoch != epoch || epoch == 0 || epoch > math.MaxInt64 || d.OwnerEpoch != epoch || n.InstallationID != d.InstallationID || d.Mode != "nodes" {
		return StoredNode{}, ErrNodeCredential
	}
	return n, nil
}

// NodeRetention answers which reported generations a node keeps, under the
// same serialization as pin promotion and placement, so a dropped generation
// cannot later gain fresh ownership. It deletes the status of the rest.
func (s *Service) NodeRetention(ctx context.Context, nodeID, connectionID string, epoch uint64, refs []sandbox.GenerationReference) (sandbox.NodeDeployment, []sandbox.GenerationRetention, error) {
	var deployment sandbox.NodeDeployment
	if len(refs) > maxReportedGenerations {
		return deployment, nil, ErrInvalidInput
	}
	grants := make([]sandbox.GenerationRetention, 0, len(refs))
	err := s.storage.WithNodes(ctx, func(tx NodeTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		n, err := s.connection(tx, d, nodeID, connectionID, epoch)
		if err != nil {
			return err
		}
		deployment.Generation = d.Generation
		spec, err := s.generationSpecification(tx, d, d.Generation)
		if err != nil {
			return err
		}
		deployment.SpecificationDigest = spec.Digest(d.Provider)
		if n.ReadyGeneration != nil {
			pin := *n.ReadyGeneration
			deployment.ServingGeneration = &pin
		}
		seen := map[uint64]bool{}
		for _, ref := range refs {
			if !validGeneration(ref.Generation) || !validDigest(ref.SpecificationDigest) || seen[ref.Generation] {
				return ErrInvalidInput
			}
			seen[ref.Generation] = true
			kept, err := tx.GenerationKept(n.ID, ref.Generation)
			if err != nil {
				return err
			}
			if kept {
				spec, err := s.generationSpecification(tx, d, ref.Generation)
				if err != nil {
					return err
				}
				if spec.Digest(d.Provider) != ref.SpecificationDigest {
					return ErrSpecificationMismatch
				}
			} else if err := tx.DeleteGenerationStatus(n.ID, ref.Generation); err != nil {
				return err
			}
			grants = append(grants, sandbox.GenerationRetention{GenerationReference: ref, Keep: kept})
		}
		return nil
	})
	if err != nil {
		return sandbox.NodeDeployment{}, nil, err
	}
	return deployment, grants, nil
}

// Heartbeat records a protocol 1 node's health, which reports readiness of its
// enrollment generation only.
func (s *Service) Heartbeat(ctx context.Context, nodeID, connectionID string, epoch uint64, health NodeHealth) error {
	return s.heartbeat(ctx, nodeID, connectionID, epoch, health, nil, 1)
}

// HeartbeatGenerations records a node's health and the preparation state of
// the generations it reports.
func (s *Service) HeartbeatGenerations(ctx context.Context, nodeID, connectionID string, epoch uint64, health NodeHealth, statuses []sandbox.GenerationStatus) error {
	if len(statuses) > maxReportedGenerations {
		return ErrInvalidInput
	}
	return s.heartbeat(ctx, nodeID, connectionID, epoch, health, statuses, 2)
}

func (s *Service) heartbeat(ctx context.Context, nodeID, connectionID string, epoch uint64, health NodeHealth, statuses []sandbox.GenerationStatus, protocol int) error {
	id, err := parseID(nodeID)
	if err != nil {
		return err
	}
	connection, err := parseID(connectionID)
	if err != nil {
		return err
	}
	health, err = normalizeHealth(health)
	if err != nil {
		return err
	}
	return s.storage.WithNodes(ctx, func(tx NodeTx) error {
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		n, err := s.connection(tx, d, id, connection, epoch)
		if err != nil {
			return err
		}
		current, err := tx.HeartbeatNode(Heartbeat{NodeID: id, ConnectionID: connection, Epoch: epoch, Health: health})
		if err != nil {
			return err
		}
		if !current {
			return ErrNodeCredential
		}
		if protocol == 1 {
			state := "failed"
			if health.ProviderReady {
				state = "ready"
			}
			statuses = []sandbox.GenerationStatus{{Generation: n.DeploymentGeneration, SpecificationDigest: n.SpecificationDigest, State: state, Diagnostic: health.Diagnostic}}
		}
		return s.recordGenerations(tx, d, n, statuses, protocol)
	})
}

func (s *Service) recordGenerations(tx NodeTx, d Record, n StoredNode, statuses []sandbox.GenerationStatus, protocol int) error {
	seen := map[uint64]bool{}
	for _, status := range statuses {
		if !validGeneration(status.Generation) || !validDigest(status.SpecificationDigest) || seen[status.Generation] || (status.State != "ready" && status.State != "preparing" && status.State != "failed") || status.State == "ready" && status.Diagnostic != "" {
			return ErrInvalidInput
		}
		seen[status.Generation] = true
		kept, err := tx.GenerationKept(n.ID, status.Generation)
		if err != nil {
			return err
		}
		// A sparse report may race collection of a skipped target. It creates no
		// readiness or pin; only the later correlated retention reply can drop it.
		if !kept {
			continue
		}
		spec, err := s.generationSpecification(tx, d, status.Generation)
		if err != nil {
			return err
		}
		if spec.Digest(d.Provider) != status.SpecificationDigest {
			return ErrSpecificationMismatch
		}
		if err := tx.UpsertGenerationStatus(GenerationStatusRecord{NodeID: n.ID, ConnectionID: n.ConnectionID, Generation: status.Generation, SpecificationDigest: status.SpecificationDigest, OwnerEpoch: d.OwnerEpoch, State: status.State, Diagnostic: sandbox.NormalizeNodeDiagnostic(status.Diagnostic)}); err != nil {
			return err
		}
		if status.State == "ready" && status.Generation == d.Generation {
			if err := tx.PromoteServingGeneration(n.ID, status.Generation); err != nil {
				return err
			}
		}
	}
	return tx.RefreshServingReadiness(n.ID, protocol)
}

// ConnectNode records a node connection for the owner epoch.
func (s *Service) ConnectNode(ctx context.Context, nodeID, connectionID string, epoch uint64) error {
	id, err := parseID(nodeID)
	if err != nil {
		return err
	}
	connection, err := parseID(connectionID)
	if err != nil {
		return err
	}
	connected, err := s.storage.ConnectNode(ctx, id, connection, epoch)
	if err != nil {
		return err
	}
	if !connected {
		return ErrNodeCredential
	}
	return nil
}

// DisconnectNode clears the node's connection when it is still this one.
func (s *Service) DisconnectNode(ctx context.Context, nodeID, connectionID string, epoch uint64) error {
	id, err := parseID(nodeID)
	if err != nil {
		return err
	}
	connection, err := parseID(connectionID)
	if err != nil {
		return err
	}
	return s.storage.DisconnectNode(ctx, id, connection, epoch)
}
