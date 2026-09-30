package store_test

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// testExecutionRequest is a test projection of configuration plus a confirmed
// Start. It is never sent over the Runtime connection. Tests which exercise
// preparation failures consume the actual control frames directly.
const testExecutionRequest = "test_execution_request"

type fixtureAdmission struct {
	prepare  proto.ExecutionPreparePayload
	handle   string
	executor string
}

func (h *dispatchHarness) executionFrame(env proto.Envelope) (proto.Envelope, bool) {
	if h.admissions == nil {
		h.admissions = make(map[string]fixtureAdmission)
	}
	switch env.Type {
	case proto.TypeExecutionPrepare:
		var prepare proto.ExecutionPreparePayload
		if env.DecodePayload(&prepare) != nil {
			h.t.Fatal("invalid execution preparation")
		}
		if prepare.Configuration.LocalEnvironment != nil || prepare.Configuration.WorkspaceReadOnly {
			return env, true
		}
		if prepare.SessionID == "" || prepare.Configuration.RunID != "" || len(prepare.Configuration.Input) != 0 {
			h.t.Fatal("preparation changed Session identity or submitted input early")
		}
		admission := fixtureAdmission{prepare: prepare, handle: uuid.NewString(), executor: "executor-" + prepare.SessionID}
		h.admissions[env.ID] = admission
		h.write(env.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: admission.handle, ExecutorID: admission.executor, Revision: 1, State: "ready"})
		return proto.Envelope{}, false
	case proto.TypeExecutionStart:
		admission, ok := h.admissions[env.ID]
		if !ok {
			return env, true
		}
		var start proto.ExecutionStartPayload
		if env.DecodePayload(&start) != nil || start.Handle != admission.handle || start.ExecutorID != admission.executor || start.RunID == "" || start.Input.Validate() != nil {
			h.t.Fatal("Start changed admission, Executor or input identity")
		}
		h.write(env.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: admission.handle, ExecutorID: admission.executor, Revision: 2, State: "started", RunID: start.RunID})
		request := admission.prepare.Configuration
		request.RunID, request.ConversationID, request.Input = start.RunID, admission.prepare.SessionID, start.Input
		projected, err := proto.NewEnvelope(testExecutionRequest, start.RunID, request)
		if err != nil {
			h.t.Fatal(err)
		}
		return projected, true
	case proto.TypeExecutionRelease:
		if _, ok := h.admissions[env.ID]; ok {
			delete(h.admissions, env.ID)
			return proto.Envelope{}, false
		}
	}
	return env, true
}
