package dispatch

import (
	"context"
	"errors"
	"fmt"
)

type shutdownAttempt struct {
	done chan struct{}
	err  error
}

// Shutdown stops admission and waits for owned cleanup. A later call retries
// only failed cleanup; caller timeouts never abandon or duplicate in-flight work.
func (r *Router) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if previous := r.shutdownAttempt; previous != nil {
		select {
		case <-previous.done:
			if previous.err == nil {
				r.mu.Unlock()
				return nil
			}
		default:
			r.mu.Unlock()
			return waitShutdown(ctx, previous)
		}
	}

	victims := r.sessionCancellationsLocked()
	if !r.closed {
		r.closed = true
		if r.runtimePreparation != nil {
			r.runtimePreparation.cancel()
		}
		// Prepared release claims exist before this signal can interrupt output.
		close(r.shutdownCh)
	}
	r.closePendingPreparationsLocked()
	executors := r.closeIdleExecutorsLocked()
	attempt := &shutdownAttempt{done: make(chan struct{})}
	r.shutdownAttempt = attempt
	// Keep the WaitGroup non-zero until all cancellation dispatch is complete.
	r.shutdownWG.Add(1)
	r.mu.Unlock()

	r.closeIdleExecutors(executors)
	go r.runShutdownAttempt(attempt, victims)
	return waitShutdown(ctx, attempt)
}

func (r *Router) runShutdownAttempt(attempt *shutdownAttempt, victims []sessionCancellation) {
	var releaseErr error
	for _, victim := range victims {
		// The cleanup operation has its own fixed native deadline. The
		// Shutdown caller's deadline only bounds its wait for this attempt.
		if err := r.awaitPreparedNativeRelease(context.Background(), victim.release, victim.attempt); err != nil {
			releaseErr = errors.Join(releaseErr, fmt.Errorf("dispatch: prepared run %s: %w", victim.runID, err))
		}
	}
	r.shutdownWG.Done()
	if releaseErr != nil {
		// A failed prepared release may leave its output consumer blocked. Do
		// not wait for all workers; retain the exact target for the next call.
		r.finishShutdownAttempt(attempt, releaseErr)
		return
	}

	r.shutdownWG.Wait()
	if err := r.closeEnvironments(context.Background(), ""); err != nil {
		attempt.err = errors.Join(attempt.err, err)
	}
	r.mu.Lock()
	if r.runtimePreparation != nil && r.runtimePreparation.uncertain {
		attempt.err = errors.Join(attempt.err, errors.New("dispatch: capability preparation remains uncertain"))
	}
	if r.workspaceWrite != nil && r.workspaceWrite.uncertain {
		attempt.err = errors.Join(attempt.err, errors.New("dispatch: local workspace write remains uncertain"))
	}
	for _, p := range r.preparations {
		if p.owns {
			cause := p.closeErr
			if cause == nil {
				cause = errors.New("cleanup has not settled")
			}
			attempt.err = errors.Join(attempt.err, fmt.Errorf("dispatch: preparation %s: %w", p.status.Handle, cause))
		}
	}
	for _, owner := range r.executors {
		cause := owner.closeErr
		if cause == nil {
			cause = errors.New("cleanup has not settled")
		}
		attempt.err = errors.Join(attempt.err, fmt.Errorf("dispatch: executor %s: %w", owner.id, cause))
	}
	close(attempt.done)
	r.mu.Unlock()
}

// closeEnvironments drains the owners of the unreleased assignments in
// environmentID, or in every Environment when it is empty, once their work
// has settled. A drained owner serves its Session again on the next bind.
func (r *Router) closeEnvironments(ctx context.Context, environmentID string) error {
	r.mu.Lock()
	var owners []*assignmentState
	for _, a := range r.assignments {
		if !a.released && a.environment != nil && (environmentID == "" || a.environmentID == environmentID) {
			owners = append(owners, a)
		}
	}
	r.mu.Unlock()
	var errs []error
	for _, a := range owners {
		a.cleanup.Lock()
		if err := a.environment.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("dispatch: Environment of Session %s: %w", a.ref.SessionID, err))
		}
		a.cleanup.Unlock()
	}
	return errors.Join(errs...)
}

func (r *Router) finishShutdownAttempt(attempt *shutdownAttempt, err error) {
	r.mu.Lock()
	attempt.err = errors.Join(attempt.err, err)
	close(attempt.done)
	r.mu.Unlock()
}

func waitShutdown(ctx context.Context, attempt *shutdownAttempt) error {
	select {
	case <-attempt.done:
		return attempt.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type sessionCancellation struct {
	runID   string
	release *preparedRelease
	attempt *preparedReleaseAttempt
}

// sessionCancellationsLocked claims release of every active Run, retrying a
// failed native attempt. Router.mu must be held.
func (r *Router) sessionCancellationsLocked() []sessionCancellation {
	victims := make([]sessionCancellation, 0, len(r.sessions))
	for _, state := range r.sessions {
		release, attempt := r.claimPreparedReleaseLocked(state, true, "", true)
		victims = append(victims, sessionCancellation{runID: state.runID, release: release, attempt: attempt})
	}
	return victims
}
