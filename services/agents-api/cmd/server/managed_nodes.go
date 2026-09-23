package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/node"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type managedNodes struct {
	setup         *managedSetup
	runtime       *execution.RuntimeProvider
	hub           *node.Hub
	admin         *api.DeploymentAuthenticator
	local         *node.AgentConfig
	closeProvider func()
}

func configureManagedNodes(s *store.Store, owner func(context.Context) error) (*managedNodes, error) {
	config, built, closeProvider, err := loadManagedRuntime()
	if err != nil {
		return nil, err
	}
	setupID := os.Getenv("AGENTS_API_SANDBOX_INSTALLATION_ID")
	if built == nil && setupID == "" {
		return nil, nil
	}
	if built != nil && setupID != "" {
		return nil, errors.New("Web sandbox setup cannot be combined with managed Runtime configuration")
	}
	if setupID != "" {
		id, err := uuid.Parse(setupID)
		if err != nil || id == uuid.Nil || id.String() != setupID {
			return nil, errors.New("sandbox installation ID must be a canonical UUID")
		}
		if os.Getenv("AGENTS_API_DAEMON_WS_URL") == "" {
			return nil, errors.New("Web sandbox setup requires daemon transport")
		}
	}
	result := &managedNodes{closeProvider: closeProvider}
	success := false
	defer func() {
		if !success {
			closeProvider()
		}
	}()
	result.admin, err = deploymentAdminAuthenticator()
	if err != nil {
		return nil, err
	}
	if setupID != "" && result.admin == nil {
		return nil, errors.New("Web sandbox setup requires deployment administrator credentials")
	}
	result.hub = node.NewHub(node.HubOptions{
		Authenticate: func(ctx context.Context, id, credential string) (node.Identity, error) {
			n, err := s.AuthenticateRuntimeNode(ctx, id, credential)
			if errors.Is(err, store.ErrRuntimeNodeCredential) {
				err = node.ErrAuthentication
			}
			return node.Identity{NodeID: n.NodeID, InstallationID: n.InstallationID, Provider: n.Provider, BackendFingerprint: n.BackendFingerprint, MaxActive: n.MaxActive, MaxRetained: n.MaxRetained}, err
		},
		OwnerEpoch: func(ctx context.Context) (uint64, error) {
			if err := owner(ctx); err != nil {
				return 0, err
			}
			return s.RuntimeOwnerEpoch(ctx)
		},
		Connected: func(ctx context.Context, n node.Identity, connection string, epoch uint64) error {
			if err := owner(ctx); err != nil {
				return err
			}
			return s.ConnectRuntimeNode(ctx, n.NodeID, connection, epoch)
		},
		Disconnected: func(ctx context.Context, n node.Identity, connection string, epoch uint64) {
			_ = s.DisconnectRuntimeNode(ctx, n.NodeID, connection, epoch)
		},
		Heartbeat: func(ctx context.Context, n node.Identity, connection string, epoch uint64, health node.Health) error {
			if err := owner(ctx); err != nil {
				return err
			}
			return s.HeartbeatRuntimeNode(ctx, n.NodeID, connection, epoch, store.RuntimeNodeHealth{ProviderReady: health.ProviderReady, Diagnostic: health.Diagnostic, CPUCount: health.CPUCount, AvailableMemoryBytes: health.AvailableMemoryBytes, AvailableDiskBytes: health.AvailableDiskBytes})
		},
	})
	if setupID != "" {
		result.setup = &managedSetup{store: s, hub: result.hub, installationID: setupID}
		result.runtime = execution.NewDeferredRuntimeProvider(setupID, result.setup.load)
		success = true
		return result, nil
	}
	result.runtime = runtimeFromConfig(config, built)
	result.runtime.Provider = result.hub.Provider(config.Provider, func(ctx context.Context, r sandbox.Reference) (string, error) {
		return s.ResolveRuntimeNode(ctx, r.TenantID, r.EnvironmentID)
	})
	if built.Provider != nil {
		result.runtime.VerifyLegacyOwnership = func(ctx context.Context, allocation store.RuntimeAllocation) error {
			return execution.VerifyLegacyRuntimeOwnership(ctx, built.Provider, allocation)
		}
		dir := os.Getenv("AGENTS_API_SANDBOX_NODE_STATE_DIR")
		if dir == "" {
			file, err := filepath.Abs(os.Getenv("AGENTS_API_MANAGED_RUNTIMES_FILE"))
			if err != nil {
				return nil, err
			}
			dir = filepath.Join(filepath.Dir(file), "sandbox-node-"+config.InstallationID+"-"+built.BackendFingerprint[:16])
		}
		maxActive, maxRetained := 4, 16
		if p := built.Suspension; p != nil {
			maxActive, maxRetained = p.MaxActive, p.MaxRetained
		}
		if n := config.Nodes; n != nil {
			maxActive, maxRetained = n.MaxActive, n.MaxRetained
		}
		origin, err := localNodeOrigin()
		if err != nil {
			return nil, err
		}
		identity, err := node.InitIdentity(dir, origin, node.Identity{InstallationID: config.InstallationID, Provider: config.Provider, BackendFingerprint: built.BackendFingerprint, MaxActive: maxActive, MaxRetained: maxRetained})
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(identity.Credential))
		result.runtime.LocalNodeID = identity.Identity.NodeID
		result.runtime.LocalCredentialSHA256 = hex.EncodeToString(digest[:])
		result.runtime.LocalMaxActive, result.runtime.LocalMaxRetained = maxActive, maxRetained
		result.local = &node.AgentConfig{CoreURL: origin, StateDirectory: dir, Identity: identity.Identity, Credential: identity.Credential, Provider: built.Provider, Probe: func(ctx context.Context) (node.Health, error) { return node.Health{}, built.Probe(ctx) }}
	}
	success = true
	return result, nil
}

func (m *managedNodes) close() {
	if m != nil {
		m.hub.Close()
		m.closeProvider()
	}
}

func deploymentAdminAuthenticator() (*api.DeploymentAuthenticator, error) {
	path := os.Getenv("AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE")
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE")
	}
	var digests []string
	if json.Unmarshal(raw, &digests) != nil || len(digests) == 0 {
		return nil, errors.New("sandbox administrator configuration must contain an array of SHA-256 digests")
	}
	return api.NewDeploymentAuthenticator(digests)
}

func serverAddress() string {
	if value := os.Getenv("AGENTS_API_ADDR"); value != "" {
		return value
	}
	return "127.0.0.1:8091"
}
func localNodeOrigin() (string, error) {
	host, port, err := net.SplitHostPort(serverAddress())
	if err != nil || port == "" || port == "0" {
		return "", errors.New("embedded sandbox node requires a fixed Core TCP listen port")
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	ip := net.ParseIP(host)
	// A listener bound exclusively to a private interface cannot be reached over
	// loopback. Its embedded agent requires the operator's HTTPS origin instead.
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		origin := os.Getenv("AGENTS_API_SANDBOX_NODE_CORE_URL")
		if origin == "" {
			return "", errors.New("non-loopback Core bind requires AGENTS_API_SANDBOX_NODE_CORE_URL with HTTPS")
		}
		return origin, nil
	}
	return "http://" + net.JoinHostPort(host, port), nil
}
