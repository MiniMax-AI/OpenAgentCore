package execution

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

const DefaultExecutionConcurrency = 4

// Worker owns queued work; the database lease excludes a second execution service.
type Worker struct {
	concurrency         int
	metrics             workerMetricsState
	dispatcher          *Dispatcher
	admission           *store.Store
	lease               *store.ExecutionLease
	directoryReads      chan directoryReadRequest
	fileWrites          chan fileWriteRequest
	stopped             chan struct{}
	stopOnce            sync.Once
	runtimes            *runtimeManager
	enrolledConnections map[string]*runtimeConnection
}

func StartWorker(ctx context.Context, dispatcher *Dispatcher) (*Worker, error) {
	if dispatcher.MaxConcurrentExecutions < 0 || dispatcher.MaxConcurrentExecutions > 1024 {
		return nil, errors.New("execution concurrency must be between 1 and 1024, or zero for the default")
	}
	lease, err := dispatcher.Store.AcquireExecutionLease(ctx)
	if err != nil {
		return nil, err
	}
	owned := *dispatcher
	owned.Store = lease.Store()
	worker := &Worker{concurrency: dispatcher.MaxConcurrentExecutions, dispatcher: &owned, admission: dispatcher.Store, lease: lease, directoryReads: make(chan directoryReadRequest), fileWrites: make(chan fileWriteRequest), stopped: make(chan struct{}), enrolledConnections: make(map[string]*runtimeConnection)}
	worker.runtimes, err = newRuntimeManager(owned.Store, owned.Registry, owned.ManagedRuntimes)
	if err != nil {
		_ = lease.Close(context.Background())
		return nil, err
	}
	var deployment *store.RuntimeDeployment
	var verify store.RuntimeOwnershipVerifier
	if worker.runtimes != nil && worker.runtimes.loadDeployment == nil {
		config := worker.runtimes.config
		verify = config.VerifyLegacyOwnership
		deployment = &store.RuntimeDeployment{ProviderKind: config.ProviderKind, LocalNodeID: config.LocalNodeID, LocalCredentialSHA256: config.LocalCredentialSHA256, LocalMaxActive: config.LocalMaxActive, LocalMaxRetained: config.LocalMaxRetained, InstallationID: config.InstallationID, BackendFingerprint: config.BackendFingerprint, Maintenance: config.Maintenance}
	}
	if worker.runtimes != nil && worker.runtimes.loadDeployment != nil {
		err = owned.Store.ClaimWebSandboxDeployment(ctx, worker.runtimes.setupInstallationID)
		if err == nil {
			_, err = worker.runtimes.ensureDeployment(ctx)
		}
	} else {
		err = owned.Store.ConfigureRuntimeDeployment(ctx, deployment, verify)
	}
	if err != nil {
		if worker.runtimes != nil {
			worker.runtimes.stop()
		}
		_ = lease.Close(context.Background())
		return nil, err
	}
	if err := owned.Store.ReconcileEnvironmentConnections(ctx); err != nil {
		if worker.runtimes != nil {
			worker.runtimes.stop()
		}
		_ = lease.Close(context.Background())
		return nil, err
	}
	if err := worker.reconcile(ctx); err != nil {
		if worker.runtimes != nil {
			worker.runtimes.stop()
		}
		_ = lease.Close(context.Background())
		return nil, err
	}
	worker.observeOwnership(nil)
	return worker, nil
}

// CheckOwnership checks the same database lease used for execution writes.
func (w *Worker) CheckOwnership(ctx context.Context) error {
	err := w.lease.Ping(ctx)
	w.observeOwnership(err)
	return err
}

func (w *Worker) SubmitInputs(ctx context.Context, tenant, session, key string, inputs []store.Input) ([]store.InputReceipt, error) {
	value, err := w.admission.GetSession(ctx, tenant, session)
	if err != nil {
		return nil, err
	}
	if err := w.dispatcher.validateEngineInputs(value.Engine, value.Configuration, inputs); err != nil {
		return nil, err
	}
	if preparedEnvironmentConfiguration(value.Configuration) {
		return w.submitEnvironmentInputs(ctx, value, key, inputs)
	}
	if !w.dispatcher.canAdmitInputs(value.Engine, value.Configuration) {
		return nil, store.ErrInvalidInput
	}
	return w.admission.SubmitInputs(ctx, tenant, session, key, inputs)
}

// CreateSession validates execution support before reserving or admitting initial work.
func (w *Worker) CreateSession(ctx context.Context, tenant string, input store.CreateSessionInput) (store.Session, error) {
	if err := w.validateCreation(ctx, input); err != nil {
		return store.Session{}, err
	}
	return w.admission.CreateSession(ctx, tenant, input)
}

// CreateSessionStream applies the same execution admission before creating a stream.
func (w *Worker) CreateSessionStream(ctx context.Context, tenant string, input store.CreateSessionInput) (store.SessionCreation, error) {
	if err := w.validateCreation(ctx, input); err != nil {
		return store.SessionCreation{}, err
	}
	return w.admission.CreateSessionStream(ctx, tenant, input)
}

// Run retains queued work across restarts, but never replays an uncertain claim.
func (w *Worker) Run(ctx context.Context) (runErr error) {
	defer w.stopOnce.Do(func() { close(w.stopped) })
	ctx, cancel := context.WithCancel(ctx)
	var running sync.WaitGroup
	defer func() {
		w.observeWorkerStop(runErr, ctx.Err())
		cancel()
		if w.runtimes != nil {
			w.runtimes.stop()
		}
		running.Wait()
		if w.runtimes != nil {
			// Drain an external provisioning caller before releasing the writer lease.
			w.runtimes.drain()
		}
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		w.observeWorkerClosed(w.lease.Close(closeCtx))
	}()
	active := make(map[string]bool)
	w.observeSlots(len(active))
	type completion struct {
		id  string
		err error
	}
	completed := make(chan completion, w.executionConcurrency())
	lifecycleDone := make(chan error, 1)
	if w.runtimes != nil {
		running.Add(1)
		go func() {
			defer running.Done()
			lifecycleDone <- w.runManagedRuntimes(ctx)
		}()
	}
	type readCompletion struct {
		id      string
		request directoryReadRequest
		result  directoryReadResult
	}
	type writeCompletion struct {
		request fileWriteRequest
		result  fileWriteResult
	}
	writesCompleted := make(chan writeCompletion, w.executionConcurrency())
	readsCompleted := make(chan readCompletion, w.executionConcurrency())
	reads := 0
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	schedule := workerSchedule{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-lifecycleDone:
			return err
		case request := <-w.fileWrites:
			if request.ctx.Err() != nil || active[request.environment.SessionID] || len(active) == w.executionConcurrency() {
				request.result <- fileWriteResult{err: ErrExecutionUnavailable}
				continue
			}
			active[request.environment.SessionID] = true
			w.observeSlots(len(active))
			running.Add(1)
			go func() {
				defer running.Done()
				writesCompleted <- writeCompletion{request: request, result: w.runFileWrite(ctx, request)}
			}()
		case write := <-writesCompleted:
			delete(active, write.request.environment.SessionID)
			w.observeSlots(len(active))
			write.request.result <- write.result
		case request := <-w.directoryReads:
			if request.ctx.Err() != nil || reads == w.executionConcurrency() || (!active[request.environment.SessionID] && len(active) == w.executionConcurrency()) {
				request.reply(directoryReadResult{err: ErrExecutionUnavailable})
				continue
			}
			reserved := !active[request.environment.SessionID]
			if reserved {
				active[request.environment.SessionID] = true
				w.observeSlots(len(active))
			}
			reads++
			running.Add(1)
			go func() {
				defer running.Done()
				result := w.runDirectoryRead(ctx, request, reserved)
				id := ""
				if reserved {
					id = request.environment.SessionID
				}
				readsCompleted <- readCompletion{id: id, request: request, result: result}
			}()
		case read := <-readsCompleted:
			reads--
			if read.id != "" {
				delete(active, read.id)
				w.observeSlots(len(active))
			}
			read.request.reply(read.result)
		case result := <-completed:
			delete(active, result.id)
			w.observeSlots(len(active))
			if result.err != nil {
				return result.err
			}
		case <-ticker.C:
			check, stop := context.WithTimeout(ctx, 5*time.Second)
			err := w.CheckOwnership(check)
			stop()
			if err != nil {
				w.observeSchedulerPoll(0, err)
				return err
			}
			if _, err := w.dispatcher.Store.ExpireEnvironmentInputs(ctx); err != nil {
				w.observeSchedulerPoll(0, err)
				return err
			}
			if err := w.observeEnrolledRuntimes(ctx); err != nil {
				w.observeSchedulerPoll(0, err)
				return err
			}
			if len(active) == w.executionConcurrency() {
				w.observeSchedulerPoll(0, nil)
				continue
			}
			devices := w.dispatcher.Registry.Devices()
			if len(devices) == 0 {
				w.observeSchedulerPoll(0, nil)
				continue
			}
			work, err := schedule.selectWork(ctx, w, devices, active)
			w.observeSlots(len(active))
			if err != nil {
				w.observeSchedulerPoll(0, err)
				return err
			}
			w.observeSchedulerPoll(len(work), nil)
			for _, item := range work {
				running.Add(1)
				go func() {
					defer running.Done()
					var err error
					if item.reservationID != "" {
						err = w.runEnvironmentInput(ctx, item)
					} else {
						err = w.runClaim(ctx, item.ExecutionWork)
					}
					completed <- completion{id: item.SessionID, err: err}
				}()
			}
		}
	}
}

func (w *Worker) reconcile(ctx context.Context) error {
	cursor := ""
	for {
		work, err := w.dispatcher.Store.ListExecutionWork(ctx, cursor, []string{store.TurnInProgress, store.TurnWaiting}, nil)
		if err != nil {
			return err
		}
		if len(work) == 0 {
			return nil
		}
		for _, item := range work {
			_, err := w.dispatcher.Store.TransitionTurn(ctx, item.TenantID, item.SessionID, item.TurnID, store.TurnTransition{ExpectedStatus: item.Status, Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_interrupted"}`)})
			if err != nil && !errors.Is(err, store.ErrTurnConflict) {
				return err
			}
			cursor = item.TurnID
		}
	}
}

func (w *Worker) runClaim(ctx context.Context, item store.ExecutionWork) error {
	_, err := w.dispatcher.Run(ctx, item.TenantID, item.SessionID, item.TurnID)
	if err == nil || errors.Is(err, store.ErrTurnConflict) {
		return nil
	}
	outcome := json.RawMessage(`{"error_code":"execution_unavailable"}`)
	if errors.Is(err, store.ErrModelProviderRequired) {
		outcome = json.RawMessage(`{"error_code":"model_provider_required"}`)
	}
	finish, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	turn, err := w.dispatcher.Store.GetTurn(finish, item.TenantID, item.SessionID, item.TurnID)
	if err != nil {
		return err
	}
	if turn.Status == store.TurnCompleted || turn.Status == store.TurnFailed || turn.Status == store.TurnCancelled {
		return nil
	}
	log.Ctx(ctx).Error("oac-core dispatch did not complete", "turn_id", item.TurnID)
	_, err = w.dispatcher.Store.TransitionTurn(finish, item.TenantID, item.SessionID, item.TurnID, store.TurnTransition{ExpectedStatus: turn.Status, Status: store.TurnFailed, Outcome: outcome})
	if errors.Is(err, store.ErrTurnConflict) {
		return nil
	}
	return err
}

func (w *Worker) executionConcurrency() int {
	if w.concurrency == 0 {
		return DefaultExecutionConcurrency
	}
	return w.concurrency
}
