package execution

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

const DefaultExecutionConcurrency = 4

// Worker owns queued work; the database lease excludes a second execution service.
type Worker struct {
	concurrency         int
	metrics             workerMetricsState
	dispatcher          *Dispatcher
	admission           *store.Store
	lease               Ownership
	directoryReads      chan directoryReadRequest
	fileWrites          chan fileWriteRequest
	scheduleWake        chan struct{}
	stopped             chan struct{}
	stopOnce            sync.Once
	runtimes            *runtimeManager
	enrolledConnections map[string]*runtimeConnection
}

// StartWorker takes over owner.Lease from the moment it is called: a failed
// start closes the lease before returning, and a started Worker closes it after
// Run drains.
func StartWorker(ctx context.Context, dispatcher *Dispatcher, owner Owner) (_ *Worker, err error) {
	if owner.Lease == nil {
		return nil, errors.New("execution worker requires an execution lease")
	}
	defer func() {
		if err == nil {
			return
		}
		if closeErr := closeLease(ctx, owner.Lease); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	if dispatcher.MaxConcurrentExecutions < 0 || dispatcher.MaxConcurrentExecutions > 1024 {
		return nil, errors.New("execution concurrency must be between 1 and 1024, or zero for the default")
	}
	if dispatcher.Credentials == nil {
		return nil, errors.New("execution worker requires MCP Credentials")
	}
	if dispatcher.Observer == nil {
		return nil, errors.New("execution requires a model configuration observer")
	}
	if dispatcher.Deployment == nil {
		return nil, errors.New("execution worker requires the deployment service")
	}
	if dispatcher.Sessions == nil {
		return nil, errors.New("execution worker requires the Session service")
	}
	if dispatcher.SessionsReader == nil {
		return nil, errors.New("execution worker requires the Session reader")
	}
	if owner.Store == nil {
		return nil, errors.New("execution worker requires the execution Store")
	}
	if owner.Deployment == nil {
		return nil, errors.New("execution worker requires the deployment execution operations")
	}
	owned := *dispatcher
	owned.Store = owner.Store
	owned.notifications = &executionNotifications{}
	worker := &Worker{concurrency: dispatcher.MaxConcurrentExecutions, dispatcher: &owned, admission: dispatcher.Store, lease: owner.Lease, directoryReads: make(chan directoryReadRequest), fileWrites: make(chan fileWriteRequest), stopped: make(chan struct{}), scheduleWake: make(chan struct{}, 1), enrolledConnections: make(map[string]*runtimeConnection)}
	worker.runtimes, err = newRuntimeManager(owner, owned.Deployment, owned.Registry, owned.ManagedRuntimes)
	if err != nil {
		return nil, err
	}
	if worker.runtimes != nil {
		defer func() {
			if err != nil {
				worker.runtimes.stop()
			}
		}()
	}
	var process *deployment.ProcessDeployment
	if worker.runtimes != nil && worker.runtimes.loadDeployment == nil {
		config := worker.runtimes.config
		process = &deployment.ProcessDeployment{ProviderKind: config.ProviderKind, LocalNodeID: config.LocalNodeID, LocalCredentialSHA256: config.LocalCredentialSHA256, LocalMaxActive: config.LocalMaxActive, LocalMaxRetained: config.LocalMaxRetained, InstallationID: config.InstallationID, BackendFingerprint: config.BackendFingerprint, AdmissionPaused: config.AdmissionPaused}
	}
	if worker.runtimes != nil && worker.runtimes.loadDeployment != nil {
		err = owner.Deployment.Claim(ctx, worker.runtimes.setupInstallationID)
		if err == nil {
			_, err = worker.runtimes.ensureDeployment(ctx)
		}
	} else {
		err = owner.Deployment.ConfigureProcess(ctx, process)
	}
	if err != nil {
		return nil, err
	}
	if err = owned.Store.ReconcileEnvironmentConnections(ctx); err != nil {
		return nil, err
	}
	if err = worker.reconcile(ctx); err != nil {
		return nil, err
	}
	worker.observeOwnership(nil)
	return worker, nil
}

// CheckOwnership checks the same database lease used for execution writes.
func (w *Worker) CheckOwnership(ctx context.Context) error {
	err := w.lease.CheckOwnership(ctx)
	w.observeOwnership(err)
	return err
}

func (w *Worker) SubmitInputs(ctx context.Context, tenant, session, key string, inputs []sessions.Input) ([]sessions.InputReceipt, error) {
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
		return nil, sessions.ErrInvalidInput
	}
	return w.admitInputs(ctx, tenant, session, key, inputs)
}

// CreateSession validates execution support before reserving or admitting initial work.
func (w *Worker) CreateSession(ctx context.Context, tenant string, input sessions.CreateSession) (sessions.Session, error) {
	if err := w.validateCreation(ctx, input); err != nil {
		return sessions.Session{}, err
	}
	session, err := w.admission.CreateSession(ctx, tenant, input)
	if err == nil && len(input.InitialInputs) > 0 {
		w.wakeScheduler()
	}
	return session, err
}

// CreateSessionStream applies the same execution admission before creating a stream.
func (w *Worker) CreateSessionStream(ctx context.Context, tenant string, input sessions.CreateSession) (sessions.Creation, error) {
	if err := w.validateCreation(ctx, input); err != nil {
		return sessions.Creation{}, err
	}
	creation, err := w.admission.CreateSessionStream(ctx, tenant, input)
	if err == nil && len(input.InitialInputs) > 0 {
		w.wakeScheduler()
	}
	return creation, err
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
		w.observeWorkerClosed(closeLease(ctx, w.lease))
	}()
	active := make(map[string]bool)
	w.observeSlots(len(active))
	type completion struct {
		id  string
		err error
	}
	completed := make(chan completion, w.executionConcurrency())
	preparationDone := make(chan error, 1)
	running.Add(1)
	go func() { defer running.Done(); preparationDone <- w.runEnvironmentInitializations(ctx) }()
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
	// A hint can encounter occupied slots or a preceding execution finishing
	// on the same Session. Allow one immediate retry when a slot is released;
	// failed preparations still wait for polling instead of spinning.
	rescanOnCompletion := false
	for {
		maintenance := false
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-preparationDone:
			return err
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
			continue
		case write := <-writesCompleted:
			delete(active, write.request.environment.SessionID)
			w.observeSlots(len(active))
			write.request.result <- write.result
			if !rescanOnCompletion {
				continue
			}
			rescanOnCompletion = false
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
			continue
		case read := <-readsCompleted:
			reads--
			if read.id != "" {
				delete(active, read.id)
				w.observeSlots(len(active))
			}
			read.request.reply(read.result)
			if read.id == "" || !rescanOnCompletion {
				continue
			}
			rescanOnCompletion = false
		case result := <-completed:
			delete(active, result.id)
			w.observeSlots(len(active))
			if result.err != nil {
				return result.err
			}
			if !rescanOnCompletion {
				continue
			}
			rescanOnCompletion = false
		case <-w.scheduleWake:
			rescanOnCompletion = true
		case <-ticker.C:
			maintenance = true
		}
		check, stop := context.WithTimeout(ctx, 5*time.Second)
		err := w.CheckOwnership(check)
		stop()
		if err != nil {
			w.observeSchedulerPoll(0, err)
			return err
		}
		if maintenance {
			if _, err := w.dispatcher.Store.ExpireEnvironmentInputs(ctx); err != nil {
				w.observeSchedulerPoll(0, err)
				return err
			}
			if err := w.observeEnrolledRuntimes(ctx); err != nil {
				w.observeSchedulerPoll(0, err)
				return err
			}
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
		if !maintenance {
			schedule.nextEnvironmentScan = time.Time{}
		}
		work, err := schedule.selectWork(ctx, w, devices, active)
		w.observeSlots(len(active))
		if err != nil {
			w.observeSchedulerPoll(0, err)
			return err
		}
		w.observeSchedulerPoll(len(work), nil)
		rescanOnCompletion = rescanOnCompletion && (len(active) > len(work) || len(active) == w.executionConcurrency())
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
				w.dispatcher.notifications.notify(item.TenantID, item.SessionID)
				completed <- completion{id: item.SessionID, err: err}
			}()
		}
	}
}

func (w *Worker) reconcile(ctx context.Context) error {
	cursor := ""
	for {
		work, err := w.dispatcher.Store.ListExecutionWork(ctx, cursor, []string{sessions.TurnInProgress, sessions.TurnWaiting}, nil)
		if err != nil {
			return err
		}
		if len(work) == 0 {
			return nil
		}
		for _, item := range work {
			_, err := w.dispatcher.Store.TransitionTurn(ctx, item.TenantID, item.SessionID, item.TurnID, sessions.TurnTransition{ExpectedStatus: item.Status, Status: sessions.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_interrupted"}`)})
			if err != nil && !errors.Is(err, sessions.ErrTurnConflict) {
				return err
			}
			cursor = item.TurnID
		}
	}
}

func (w *Worker) runClaim(ctx context.Context, item sessions.ExecutionWork) error {
	_, err := w.dispatcher.Run(ctx, item.TenantID, item.SessionID, item.TurnID)
	if err == nil || errors.Is(err, sessions.ErrTurnConflict) {
		return nil
	}
	var rejection *preparationRejection
	capacityRejected := errors.As(err, &rejection) && rejection.operation == proto.TypeExecutionPrepare && rejection.code == "preparation_capacity"
	outcome := json.RawMessage(`{"error_code":"execution_unavailable"}`)
	if errors.Is(err, ErrModelProviderRequired) {
		outcome = json.RawMessage(`{"error_code":"model_provider_required"}`)
	}
	finish, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	turn, err := w.dispatcher.Store.GetTurn(finish, item.TenantID, item.SessionID, item.TurnID)
	if err != nil {
		return err
	}
	if turn.Status == sessions.TurnQueued && capacityRejected {
		// No input was sent. Leave durable work for the existing scheduler tick;
		// active and cleanup-held Runtime capacity have the same rejection.
		return nil
	}
	if turn.Status == sessions.TurnCompleted || turn.Status == sessions.TurnFailed || turn.Status == sessions.TurnCancelled {
		return nil
	}
	log.Ctx(ctx).Error("oac-core dispatch did not complete", "turn_id", item.TurnID)
	_, err = w.dispatcher.Store.TransitionTurn(finish, item.TenantID, item.SessionID, item.TurnID, sessions.TurnTransition{ExpectedStatus: turn.Status, Status: sessions.TurnFailed, Outcome: outcome})
	if errors.Is(err, sessions.ErrTurnConflict) {
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
