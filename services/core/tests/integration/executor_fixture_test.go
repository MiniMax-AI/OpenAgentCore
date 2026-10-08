package integration

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// testExecutionRequest is a test projection of configuration plus a confirmed
// Start. It is never sent over the Runtime connection. Tests which exercise
// preparation failures consume the actual control frames directly.
const testExecutionRequest = "test_execution_request"

// testExecution is the payload of a testExecutionRequest.
type testExecution struct {
	proto.PromptRequestPayload
	RunID string             `json:"run_id"`
	Input proto.MessageInput `json:"input"`
}

type fixtureAdmission struct {
	prepare  proto.ExecutionPreparePayload
	handle   string
	executor string
}

// assignmentReply is the reply of a Runtime that binds every assignment to
// env, when env is an assignment_bind.
func assignmentReply(env proto.Envelope) (proto.Envelope, bool) {
	if env.Type != proto.TypeAssignmentBind {
		return proto.Envelope{}, false
	}
	reply, err := env.Reply(proto.TypeAssignmentStatus, proto.AssignmentStatusPayload{State: proto.AssignmentBound})
	return reply, err == nil
}

// observe remembers the assignment env names for its ID and started Run. The
// fixture Runtime writes its frames under it, as a Runtime echoes its
// request's assignment.
func (h *dispatchHarness) observe(env proto.Envelope) {
	if !env.Assignment.Valid() {
		return
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if h.assignments == nil {
		h.assignments = make(map[string]proto.AssignmentRef)
	}
	h.assignment, h.assignments[env.ID] = env.Assignment, env.Assignment
	var start proto.ExecutionStartPayload
	if env.Type == proto.TypeExecutionStart && env.DecodePayload(&start) == nil && start.RunID != "" {
		h.assignments[start.RunID] = env.Assignment
	}
}

// assignmentFrame observes env, answers an assignment_bind and reports whether
// env was one.
func (h *dispatchHarness) assignmentFrame(env proto.Envelope) bool {
	h.observe(env)
	reply, ok := assignmentReply(env)
	if !ok {
		return false
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if err := h.conn.WriteJSON(reply); err != nil {
		h.t.Fatal(err)
	}
	return true
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
		if prepare.SessionID == "" {
			h.t.Fatal("preparation lost Session identity")
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
		projected, err := proto.NewEnvelope(testExecutionRequest, start.RunID, testExecution{PromptRequestPayload: admission.prepare.Configuration, RunID: start.RunID, Input: start.Input})
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
