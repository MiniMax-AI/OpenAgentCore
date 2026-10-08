package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// fixtureGateway composes the daemon transport as cmd/server does.
func fixtureGateway(t *testing.T, s *Store, publicWSURL string) (http.Handler, *runtimegateway.Registry) {
	t.Helper()
	registry := runtimegateway.NewRegistry()
	h := runtimegateway.NewHandler(runtimegateway.HandlerConfig{
		Authenticator: runtimegateway.NewAuthenticator(sessionAdapter(s)), Registry: registry,
		Heartbeat: sessionService(t, s), Links: runtimegateway.NewLinkAuthority(sessionAdapter(s)), PublicWSURL: publicWSURL,
	})
	handler, err := publicHandler(t, s, newTestAuthenticator(t, nil), "codex", func(d *api.Dependencies) {
		d.Execution.RuntimeConnect = http.HandlerFunc(h.WS)
		d.Execution.Bootstrap = http.HandlerFunc(h.Bootstrap)
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, registry
}

type dispatchHarness struct {
	writeMu      sync.Mutex
	assignments  map[string]proto.AssignmentRef // by frame and Run ID; writeMu guards it
	assignment   proto.AssignmentRef            // the latest assignment Core named
	admissions   map[string]fixtureAdmission
	t            *testing.T
	s            *Store
	d            *execution.Dispatcher
	lease        execution.Ownership // held by tests that run execution operations without a Worker
	owned        *execution.Owner    // the Owner that bound binds, acquired on first use
	tenant       string
	session      sessions.Session
	device       sessions.ExecutionDevice
	conn         *websocket.Conn
	registry     *runtimegateway.Registry
	url          string
	credential   string
	environments map[string]*dispatchHarness
	link         *sandboxlinktest.Server   // the relay the Session's Environment Serves at
	resource     sandboxbootstrap.Resource // the Environment's Link resource, if the Session has one
	serve        []byte                    // the resource's Serve credential
	served       *linkServe                // the fake sandbox Serving resource
}

func newDispatchHarness(t *testing.T) *dispatchHarness {
	t.Helper()
	return newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model","instructions":"Keep this instruction."},"environment":{"type":"none"}}`))
}

// newDispatchHarnessForSession binds a Session of configuration to the
// deployment's agent host, which the harness connects as. A Session with an
// Environment first gets a live Link resource that a fake sandbox Serves at
// the harness's relay: an enrollment for self_hosted, an allocation otherwise.
func newDispatchHarnessForSession(t *testing.T, configuration []byte) *dispatchHarness {
	t.Helper()
	var snapshot struct {
		Environment struct {
			Type string `json:"type"`
		} `json:"environment"`
	}
	_ = json.Unmarshal(configuration, &snapshot)
	var s *Store
	if snapshot.Environment.Type == "openai_hosted" {
		s, _ = configuredStore(t)
	} else {
		s, _ = newManagedTestStore(t)
	}
	h := &dispatchHarness{t: t, s: s, tenant: uuid.NewString(), environments: map[string]*dispatchHarness{}}
	ctx := context.Background()
	var err error
	h.session, err = s.CreateSession(ctx, h.tenant, WithFixtureModelProvider(sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "session", Configuration: configuration}))
	if err != nil {
		t.Fatal(err)
	}
	host := registerAgentHost(t, s)
	h.device, h.credential = sessions.ExecutionDevice{ID: host.ID, Name: "agent host"}, host.Credential
	secret := h.credential
	h.link = sandboxlinktest.StartRelay(t, runtimegateway.NewLinkAuthority(sessionAdapter(s)))
	if h.session.Environment != nil {
		h.resource, h.serve = fixtureLinkResource(t, s, h.tenant, h.session)
		h.served = startLinkServe(t, h.link, h.serve, h.resource.Ref())
		within(t, h.served.connected)
	}
	if err = bindSessionDevice(t, s, h.tenant, h.session.ID, h.device.ID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	wsURL := "ws://" + server.Listener.Addr().String() + "/api/v1/agent-daemon/ws"
	server.Config.Handler, h.registry = fixtureGateway(t, s, wsURL)
	server.Start()
	h.url = server.URL
	t.Cleanup(func() { server.Close(); h.registry.CloseConnections() })
	u, _ := url.Parse(wsURL)
	u.RawQuery = url.Values{"device_id": {h.device.ID}, "version": {proto.Version}}.Encode()
	h.conn, _, err = websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": {"Bearer " + secret}})
	if err != nil {
		t.Fatal("device connection failed")
	}
	t.Cleanup(func() { h.conn.Close() })
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{HomeRemoval: proto.CapabilityUnsupported, SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{TextVerbosity: proto.CapabilitySupported, SubagentObservations: proto.CapabilitySupported, NativeSessionRecovery: proto.CapabilitySupported, EnvironmentNone: proto.CapabilitySupported})}}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		peer, e := h.registry.LookupDevice(h.device.ID)
		if e == nil {
			if _, _, known := peer.AgentKindStatus("codex"); known {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	sessionStore := sessionAdapter(s)
	sessionService, err := newSessionService(s)
	if err != nil {
		t.Fatal(err)
	}
	h.d = &execution.Dispatcher{Registry: h.registry, Links: h.link.Relay, Observer: modelconfigurationpg.New(pgunit.NewPool(s.pool), s.credentialCipher), Sessions: sessionService, SessionsReader: sessionStore}
	return h
}

// stopServing stops the fake sandbox and waits until the relay no longer
// holds its serve peer.
func (h *dispatchHarness) stopServing() {
	h.t.Helper()
	h.served.stop()
	awaitDaemonRemoteCondition(h.t, h.t.Context(), linkWait, "the relay to drop the serve peer", func() bool { return !h.link.Relay.Serving(h.resource.Ref()) })
}

func (h *dispatchHarness) message(key, text string) sessions.InputReceipt {
	h.t.Helper()
	r, err := sendMessage(context.Background(), h.s, h.tenant, h.session.ID, key, messageText(text))
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *dispatchHarness) write(run, kind string, payload any) {
	h.t.Helper()
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if status, ok := payload.(proto.PreparationStatusPayload); ok && status.Handle != "" && status.ExecutorID == "" {
		status.ExecutorID = "executor-" + status.Handle
		payload = status
	}
	env, err := proto.NewEnvelope(kind, run, payload)
	if err != nil {
		h.t.Fatal(err)
	}
	if ref, ok := h.assignments[run]; ok {
		env.Assignment = ref
	} else if kind != proto.TypeHeartbeat {
		env.Assignment = h.assignment
	}
	if err = h.conn.WriteJSON(env); err != nil {
		h.t.Fatal(err)
	}
}

func (h *dispatchHarness) read(kind string) proto.Envelope {
	h.t.Helper()
	_ = h.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var env proto.Envelope
		if err := h.conn.ReadJSON(&env); err != nil {
			h.t.Fatal(err)
		}
		h.observe(env)
		if kind != proto.TypeAssignmentBind && h.assignmentFrame(env) {
			continue
		}
		if kind == testExecutionRequest {
			var keep bool
			env, keep = h.executionFrame(env)
			if !keep {
				continue
			}
		}
		if env.Type == kind {
			return env
		}
	}
}

type runResult struct {
	turn sessions.Turn
	err  error
}

// owner returns the harness's execution Owner. The first call acquires the
// execution lease, which closes when the test ends. A test that also starts a
// Worker hands it this Owner through startOwnedWorker.
func (h *dispatchHarness) owner() execution.Owner {
	h.t.Helper()
	if h.owned == nil {
		owner := executionOwner(h.t, h.s)
		h.owned = &owner
	}
	return *h.owned
}

// bound returns h.d bound to the harness's execution Owner, as StartWorker
// binds a Worker's Dispatcher.
func (h *dispatchHarness) bound() *execution.Dispatcher {
	h.t.Helper()
	d, err := h.d.Bind(h.owner())
	if err != nil {
		h.t.Fatal(err)
	}
	return d
}

func (h *dispatchHarness) run(ctx context.Context, turn string) <-chan runResult {
	h.t.Helper()
	d := h.bound()
	out := make(chan runResult, 1)
	go func() { result, err := d.Run(ctx, h.tenant, h.session.ID, turn); out <- runResult{result, err} }()
	return out
}

func (h *dispatchHarness) finished(result <-chan runResult, status string) sessions.Turn {
	h.t.Helper()
	select {
	case got := <-result:
		if got.err != nil || got.turn.Status != status {
			h.t.Fatalf("execution result: status=%s err=%v outcome=%s", got.turn.Status, got.err, got.turn.Outcome)
		}
		return got.turn
	case <-time.After(10 * time.Second):
		h.t.Fatal("execution did not finish")
	}
	return sessions.Turn{}
}

func TestExecutionDispatchSteeringAndNativeContinuity(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	first := h.message("first", "Initial input")
	result := h.run(ctx, first.TurnID)
	request := h.read(testExecutionRequest)
	var prompt testExecution
	_ = request.DecodePayload(&prompt)
	if inputTextForTest(t, prompt.Input) != "Initial input" || prompt.Model != "test-model" || prompt.SystemPrompt != "Keep this instruction." {
		t.Fatalf("wrong resolved request: %+v", prompt)
	}
	if _, err := h.bound().Run(ctx, uuid.NewString(), h.session.ID, first.TurnID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatalf("foreign execution: %v", err)
	}
	if _, err := h.bound().Run(ctx, h.tenant, h.session.ID, first.TurnID); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatalf("duplicate execution: %v", err)
	}
	second := h.message("second", "Follow-up input")
	steer := h.read(proto.TypePromptSteer)
	var input proto.PromptSteerPayload
	_ = steer.DecodePayload(&input)
	if inputTextForTest(t, input.Input) != "Follow-up input" {
		t.Fatal(inputTextForTest(t, input.Input))
	}
	h.write(first.TurnID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: input.InputID, ErrorCode: "not_ready"})
	retry := h.read(proto.TypePromptSteer)
	if string(retry.Payload) != string(steer.Payload) {
		t.Fatal("steering retry changed identity")
	}
	h.write(first.TurnID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: input.InputID, Accepted: true})
	h.write(first.TurnID, proto.TypeDone, proto.DonePayload{Usage: proto.Usage{InputTokens: 7, OutputTokens: 3}, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native-thread-1"}})
	done := h.finished(result, sessions.TurnCompleted)
	var outcome execution.Result
	_ = json.Unmarshal(done.Outcome, &outcome)
	if outcome.AppliedThrough != second.Sequence || outcome.Done.Usage.InputTokens != 7 {
		t.Fatalf("missing result: %+v", outcome)
	}
	// Restart Core: a new Store and execution owner continue the native Session.
	awaitRelease := pgtest.ObserveExecutionLeaseRelease(t, h.s.pool)
	if err := h.owner().Lease.Close(ctx); err != nil {
		t.Fatal(err)
	}
	awaitRelease()
	newStore := reopenStore(t, h.s)
	h.s, h.owned = newStore, nil
	bound, err := sessionAdapter(newStore).GetSessionExecutionBinding(ctx, h.tenant, h.session.ID)
	if err != nil || bound.NativeSessionID != "native-thread-1" {
		t.Fatalf("native binding lost: %+v %v", bound, err)
	}
	next := h.message("third", "Continue the session")
	result = h.run(ctx, next.TurnID)
	request = h.read(testExecutionRequest)
	_ = request.DecodePayload(&prompt)
	if prompt.AgentSessionID != "native-thread-1" {
		t.Fatal("native continuity lost")
	}
	h.write(next.TurnID, proto.TypeDone, proto.DonePayload{})
	h.finished(result, sessions.TurnCompleted)
}

func TestExecutionCancellationRequiresReceiptAndSurvivesContextEnd(t *testing.T) {
	for _, withDone := range []bool{false, true} {
		t.Run(map[bool]string{false: "receipt", true: "done-before-receipt"}[withDone], func(t *testing.T) {
			h := newDispatchHarness(t)
			first := h.message("first", "Run")
			result := h.run(context.Background(), first.TurnID)
			h.read(testExecutionRequest)
			_, err := requestCancel(context.Background(), h.s, h.tenant, h.session.ID, "cancel")
			if err != nil {
				t.Fatal(err)
			}
			env := h.read(proto.TypePromptCancel)
			var cancel proto.PromptCancelPayload
			_ = env.DecodePayload(&cancel)
			if cancel.DeliveryID == "" {
				t.Fatal("cancellation has no receipt identity")
			}
			current, err := sessionAdapter(h.s).GetTurn(context.Background(), h.tenant, h.session.ID, first.TurnID)
			if err != nil || current.Status != sessions.TurnInProgress {
				t.Fatal("cancel finished before receipt")
			}
			if withDone {
				h.write(first.TurnID, proto.TypeDone, proto.DonePayload{})
			}
			outcome := &proto.DonePayload{Metadata: map[string]any{proto.DoneMetaAgentSessionID: "cancelled-native"}, Usage: proto.Usage{InputTokens: 10}}
			h.write(first.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: cancel.DeliveryID, Applied: true, Outcome: outcome})
			h.finished(result, sessions.TurnCancelled)
			next := h.message("next", "Continue after cancellation")
			result = h.run(context.Background(), next.TurnID)
			env = h.read(testExecutionRequest)
			var prompt proto.PromptRequestPayload
			_ = env.DecodePayload(&prompt)
			if prompt.AgentSessionID != "cancelled-native" {
				t.Fatal("cancellation lost native continuity")
			}
			h.write(next.TurnID, proto.TypeDone, proto.DonePayload{})
			h.finished(result, sessions.TurnCompleted)
		})
	}
	h := newDispatchHarness(t)
	first := h.message("first", "Run")
	ctx, cancel := context.WithCancel(context.Background())
	result := h.run(ctx, first.TurnID)
	h.read(testExecutionRequest)
	cancel()
	done := h.finished(result, sessions.TurnFailed)
	var outcome execution.Result
	_ = json.Unmarshal(done.Outcome, &outcome)
	if outcome.ErrorCode != "execution_interrupted" {
		t.Fatal(outcome.ErrorCode)
	}
}

func TestExecutionFailureDoesNotBecomeSuccessOrReplay(t *testing.T) {
	for _, kind := range []string{"disconnect", "engine", "late-input", "unconfirmed-input"} {
		t.Run(kind, func(t *testing.T) {
			h := newDispatchHarness(t)
			first := h.message("first", "Run")
			result := h.run(context.Background(), first.TurnID)
			h.read(testExecutionRequest)
			switch kind {
			case "disconnect":
				h.conn.Close()
			case "engine":
				h.write(first.TurnID, proto.TypeUsage, proto.UsagePayload{Usage: proto.Usage{InputTokens: 13, OutputTokens: 7}})
				h.write(first.TurnID, proto.TypeError, proto.ErrorPayload{Error: "Engine failed"})
				h.write(first.TurnID, proto.TypeDone, proto.DonePayload{})
			case "late-input":
				h.message("late", "Still unprocessed")
				h.write(first.TurnID, proto.TypeDone, proto.DonePayload{})
			case "unconfirmed-input":
				h.message("second", "Steer")
				h.read(proto.TypePromptSteer)
				h.write(first.TurnID, proto.TypeDone, proto.DonePayload{})
			}
			done := h.finished(result, sessions.TurnFailed)
			if kind == "engine" {
				var outcome execution.Result
				_ = json.Unmarshal(done.Outcome, &outcome)
				if outcome.Done.Usage.InputTokens != 13 || outcome.Done.Usage.OutputTokens != 7 {
					t.Fatal("failed execution lost reported usage")
				}
			}
			if kind != "disconnect" {
				if _, err := h.bound().Run(context.Background(), h.tenant, h.session.ID, first.TurnID); !errors.Is(err, sessions.ErrTurnConflict) {
					t.Fatalf("terminal replay: %v", err)
				}
			}
		})
	}
}

func TestExecutionOutcomeAndNativeBindingCommitTogether(t *testing.T) {
	h := newDispatchHarness(t)
	first := h.message("first", "Run")
	ctx := context.Background()
	_, err := transitionTurn(ctx, h.s, h.tenant, h.session.ID, first.TurnID, sessions.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress})
	if err != nil {
		t.Fatal(err)
	}
	late := h.message("second", "Late")
	operations := h.owner().Sessions
	if _, err := operations.CompleteExecution(ctx, h.tenant, h.session.ID, first.TurnID, sessions.TurnCompleted, []byte(`{}`), "native-one", first.Sequence); !errors.Is(err, sessions.ErrUnappliedInputs) {
		t.Fatalf("unapplied completion: %v", err)
	}
	bound, _ := sessionAdapter(h.s).GetSessionExecutionBinding(ctx, h.tenant, h.session.ID)
	if bound.NativeSessionID != "" {
		t.Fatal("native ID committed without outcome")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, native := range []string{"native-one", "native-two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := operations.CompleteExecution(ctx, h.tenant, h.session.ID, first.TurnID, sessions.TurnCompleted, []byte(`{}`), native, late.Sequence)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, sessions.ErrTurnConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("multiple terminal owners")
	}
}

func TestExecutionRejectsRuntimeMissingCapabilityBeforeClaim(t *testing.T) {
	for _, tc := range []struct{ missing, message string }{
		{"text_verbosity", "The harness supports medium text verbosity only."},
		{"environment_none", "The harness does not support environment none."},
	} {
		missing := tc.missing
		t.Run(missing, func(t *testing.T) {
			// Only an explicit non-medium verbosity needs text_verbosity.
			h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model","instructions":"Keep this instruction.","text":{"verbosity":"high"}},"environment":{"type":"none"}}`))
			caps := prototest.Capabilities(proto.AgentKindCapabilities{TextVerbosity: proto.CapabilityFromBool(missing != "text_verbosity"), EnvironmentNone: proto.CapabilityFromBool(missing != "environment_none")})
			h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{HomeRemoval: proto.CapabilityUnsupported, SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
			deadline := time.Now().Add(3 * time.Second)
			for {
				peer, _ := h.registry.LookupDevice(h.device.ID)
				info, _, _ := peer.AgentKindStatus("codex")
				if info.Capabilities == caps {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("heartbeat not registered")
				}
				time.Sleep(10 * time.Millisecond)
			}
			first := h.message("missing-capability", "Run")
			if _, err := h.bound().Run(context.Background(), h.tenant, h.session.ID, first.TurnID); err == nil || err.Error() != tc.message {
				t.Fatalf("Run error = %v, want %q", err, tc.message)
			}
			turn, err := sessionAdapter(h.s).GetTurn(context.Background(), h.tenant, h.session.ID, first.TurnID)
			if err != nil || turn.Status != sessions.TurnQueued {
				t.Fatal("Runtime without a required capability claimed work")
			}
		})
	}
}
