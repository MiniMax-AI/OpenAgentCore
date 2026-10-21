package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// preparationCache reuses the result of an earlier preparation with the same key
// through a Provider that declares the operations. The Provider owns storage and
// lifetime; Core only computes the key.
type preparationCache struct {
	provider  sandbox.SandboxProvider
	reference sandbox.Reference
	key       string
}

// preparationCache returns nil when this Environment cannot use the cache: no
// preparation work, no declared operations or an allocation of another installation.
func (w *Worker) preparationCache(ctx context.Context, owner sessions.EnvironmentInitialization, fileCount int, operations []runtimeSetupOperation) (*preparationCache, error) {
	provider, installation := w.runtimes.provider()
	if fileCount+len(operations) == 0 || provider == nil || providercontract.Require(provider, "RestorePreparation") != nil {
		return nil, nil
	}
	allocation, err := w.dispatcher.DeploymentReader.EnvironmentAllocation(ctx, deployment.AllocationKey{TenantID: owner.TenantID, EnvironmentID: owner.EnvironmentID})
	if errors.Is(err, deployment.ErrNotFound) || err == nil && (allocation.ProviderKey != installation || allocation.State != "running") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	_ = encoder.Encode(owner.TenantID)
	for index := range fileCount {
		metadata, body, err := w.dispatcher.SessionsReader.ReadInitialEnvironmentFile(ctx, owner.TenantID, owner.SessionID, index)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(body)
		_ = encoder.Encode([]string{metadata.Path, hex.EncodeToString(digest[:])})
	}
	for _, operation := range operations {
		digest := sha256.Sum256(operation.Data)
		_ = encoder.Encode(operation.Request)
		_ = encoder.Encode(hex.EncodeToString(digest[:]))
	}
	return &preparationCache{provider, runtimeReference(allocation), hex.EncodeToString(hash.Sum(nil))}, nil
}

// run restores a saved preparation and sends only finalize, which binds the
// restored installation to this Session. Otherwise it prepares and saves the
// result. A failed save never fails the Session.
func (c *preparationCache) run(ctx context.Context, prepare, finalize func() error) error {
	if c == nil {
		return prepare()
	}
	started := time.Now()
	restored, err := c.provider.RestorePreparation(ctx, c.reference, c.key)
	c.log(ctx, "restore_preparation", restored, started, err)
	if err != nil {
		return err
	}
	if restored {
		return finalize()
	}
	if err := prepare(); err != nil {
		return err
	}
	started = time.Now()
	save := ctx
	if deadline, bounded := ctx.Deadline(); bounded {
		// Half the remaining budget, so a slow save never takes the time completion needs.
		var cancel context.CancelFunc
		save, cancel = context.WithTimeout(ctx, time.Until(deadline)/2)
		defer cancel()
	}
	c.log(ctx, "save_preparation", false, started, c.provider.SavePreparation(save, c.reference, c.key))
	return nil
}

func (c *preparationCache) log(ctx context.Context, step string, hit bool, started time.Time, err error) {
	log.Ctx(ctx).Info("Environment preparation cache", "allocation_id", c.reference.AllocationID, "step", step, "hit", hit,
		"duration_ms", time.Since(started).Milliseconds(), "succeeded", err == nil)
}
