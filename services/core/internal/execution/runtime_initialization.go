package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// The leased Worker owns preparation for every Environment. Compute managers
// never run initialization. Bounded concurrent owners prevent one slow machine
// from blocking other environments or resource reclamation.
func (w *Worker) runEnvironmentInitializations(ctx context.Context) error {
	active := make(map[string]bool)
	done := make(chan string, w.executionConcurrency())
	var running sync.WaitGroup
	ctx, stop := context.WithCancel(ctx)
	defer func() { stop(); running.Wait() }()
	cursor := ""
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case id := <-done:
			delete(active, id)
		case <-ticker.C:
		}
		rows, err := w.dispatcher.SessionsReader.ListEnvironmentInitializations(ctx, cursor)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			cursor = ""
		}
		for _, owner := range rows {
			cursor = owner.EnvironmentID
			if active[owner.EnvironmentID] {
				continue
			}
			if owner.State == "running" {
				// Lost process-local progress cannot prove which side effects ran.
				if err := w.dispatcher.sessionExecution.FailEnvironmentInitialization(ctx, owner, sessions.ProvisioningFailure{}); err != nil && !errors.Is(err, sessions.ErrNotFound) {
					return err
				}
				continue
			}
			if len(active) >= w.executionConcurrency() {
				continue
			}
			// The agent host prepares an Environment through its Link
			// resource, so one without a live resource Serving waits, even
			// while its Session stays bound.
			if !owner.ResourceLive || !w.dispatcher.Links.Serving(owner.Resource.Ref()) {
				continue
			}
			if owner.DeviceID == "" {
				// Placement binds an agent host once the Environment serves;
				// a later scan claims the preparation on it.
				if _, err := w.place(ctx, owner.TenantID, owner.SessionID, owner.EnvironmentID, func(id string) bool {
					// The Turn path's predicate: the host advertises the
					// Harness as available.
					peer, err := w.dispatcher.authorizedPeer(ctx, id)
					if err != nil {
						return false
					}
					_, err = runtimeDeclaration(peer, owner.Engine)
					return err == nil
				}); err != nil && !errors.Is(err, sessions.ErrNotFound) && !errors.Is(err, sessions.ErrDeviceBindingConflict) {
					return err
				}
				continue
			}
			peer, err := w.dispatcher.authorizedPeer(ctx, owner.DeviceID)
			if err != nil {
				if errors.Is(err, sessions.ErrNotFound) || errors.Is(err, runtimegateway.ErrSessionClosed) || errors.Is(err, runtimegateway.ErrDeviceNotRegistered) {
					continue
				}
				return err
			}
			harness, found, known := peer.AgentKindStatus(owner.Engine)
			if !known {
				continue
			}
			if !found || !harness.Available {
				if err := w.dispatcher.sessionExecution.FailEnvironmentInitialization(ctx, owner, sessions.ProvisioningFailure{Step: sessions.ProvisioningHarness}); err != nil && !errors.Is(err, sessions.ErrNotFound) {
					return err
				}
				continue
			}
			if err := w.dispatcher.sessionExecution.ClaimEnvironmentInitialization(ctx, owner); err != nil {
				if errors.Is(err, sessions.ErrNotFound) || errors.Is(err, sessions.ErrTurnConflict) {
					continue
				}
				return err
			}
			active[owner.EnvironmentID] = true
			running.Add(1)
			go func(owner sessions.EnvironmentInitialization) {
				defer running.Done()
				w.initializeEnvironment(ctx, owner)
				done <- owner.EnvironmentID
			}(owner)
		}
	}
}

func (w *Worker) initializeEnvironment(ctx context.Context, owner sessions.EnvironmentInitialization) {
	operation, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	failure := sessions.ProvisioningFailure{}
	err := w.prepareEnvironment(operation, owner, &failure)
	if err == nil {
		err = w.dispatcher.sessionExecution.CompleteEnvironmentInitialization(operation, owner)
	}
	if err == nil {
		return
	}
	record, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	// Nothing took effect, so a later scan starts the preparation again.
	if errors.Is(err, errBeforeEffect) && w.dispatcher.sessionExecution.UnclaimEnvironmentInitialization(record, owner) == nil {
		return
	}
	log.Warn(ctx, "Environment preparation failed", "environment_id", owner.EnvironmentID, "session_id", owner.SessionID)
	// A later scan settles an unrecorded failure; it never retries the setup.
	_ = w.dispatcher.sessionExecution.FailEnvironmentInitialization(record, owner, failure)
}

// errBeforeEffect ends a preparation before any of its steps took effect: its
// bind failed because the Runtime is gone or the Environment has no live Link
// resource, or the Runtime rejected its first step with resource_unavailable.
var errBeforeEffect = errors.New("environment preparation ended before any effect")

func (w *Worker) prepareEnvironment(ctx context.Context, owner sessions.EnvironmentInitialization, failure *sessions.ProvisioningFailure) error {
	environment, err := w.dispatcher.SessionsReader.GetEnvironment(ctx, owner.TenantID, owner.EnvironmentID)
	if err != nil {
		return err
	}
	var cfg struct {
		Files []environmentconfig.InitialFileMetadata `json:"files"`
	}
	if json.Unmarshal(environment.Configuration, &cfg) != nil || len(cfg.Files) > 50 {
		return sessions.ErrInvalidInput
	}
	setup, err := w.dispatcher.SessionsReader.ReadEnvironmentSetup(ctx, owner.TenantID, owner.SessionID)
	if err != nil {
		return err
	}
	peer, err := w.dispatcher.assignedPeer(ctx, sessions.ExecutionDevice{ID: owner.DeviceID, Assignment: owner.Assignment, SessionEnvironmentID: owner.EnvironmentID})
	if errors.Is(err, runtimegateway.ErrNoLinkResource) || errors.Is(err, runtimegateway.ErrSessionClosed) || errors.Is(err, runtimegateway.ErrDeviceNotRegistered) {
		return fmt.Errorf("%w: %w", errBeforeEffect, err)
	}
	if err != nil {
		return err
	}
	identity := agentcapabilities.Identity{EnvironmentID: owner.EnvironmentID, SessionID: owner.SessionID}
	operations := setupOperations(setup)
	for index := 0; index < len(cfg.Files)+len(operations); index++ {
		step, stop := context.WithTimeout(ctx, 2*time.Minute)
		err = w.lease.CheckOwnership(step)
		if err == nil {
			var currentPeer = peer
			currentPeer, err = w.dispatcher.authorizedPeer(step, owner.DeviceID)
			if err == nil && currentPeer != peer {
				err = errors.New("Runtime connection changed during initialization")
			}
		}
		candidate := sessions.ProvisioningFailure{Step: sessions.ProvisioningInitialFile}
		if err == nil && index < len(cfg.Files) {
			var metadata environmentconfig.InitialFileMetadata
			var body []byte
			metadata, body, err = w.dispatcher.SessionsReader.ReadInitialEnvironmentFile(step, owner.TenantID, owner.SessionID, index)
			if err == nil {
				err = installInitialFile(step, peer, owner.Assignment, identity, metadata, body)
			}
		} else if err == nil {
			command := operations[index-len(cfg.Files)]
			candidate = command.provisioningFailure(0)
			err = runRuntimeSetup(step, peer, owner.Assignment, identity, command)
		}
		stop()
		if err != nil {
			var confirmed *runtimeStepFailure
			if errors.As(err, &confirmed) {
				if index == 0 && confirmed.unavailable {
					return fmt.Errorf("%w: %w", errBeforeEffect, err)
				}
				candidate.ExitCode = confirmed.exitCode
				*failure = candidate
			}
			return err
		}
	}
	return nil
}
