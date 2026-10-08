package dispatch_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
)

type functionSession struct {
	*fakeSession
	mu    sync.Mutex
	calls int
}

func (s *functionSession) SubmitFunctionResult(_ context.Context, p proto.FunctionResultPayload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.CallID != "call" || s.calls != 0 {
		return agent.ErrUnknownFunctionCall
	}
	s.calls++
	return nil
}
func TestFunctionReceiptsScopeRetriesAndConflicts(t *testing.T) {
	reg := agent.NewRegistry()
	sender := &recSender{}
	sessions := map[string]*functionSession{}
	registerSession(reg, proto.SupportedAgentKind{Kind: "function-test", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{FunctionTools: proto.CapabilitySupported, FunctionResultImages: proto.CapabilitySupported})}, func(ctx context.Context, p fixtureRun, out chan<- proto.Envelope) (fixtureSession, error) {
		s := &functionSession{fakeSession: &fakeSession{out: out, ctx: ctx, closeOutOnCancel: true}}
		sessions[p.RunID] = s
		return s, nil
	})
	router, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender})
	if err != nil {
		t.Fatal(err)
	}
	defer router.Shutdown(context.Background())
	for _, id := range []string{"one", "two"} {
		startRun(t, router, sender, "function-test", id)
	}
	submit := func(run, call, text, delivery string) proto.InteractionDecisionAckPayload {
		t.Helper()
		env := scoped(t, run, proto.TypeFunctionResult, run, proto.FunctionResultPayload{CallID: call, Success: true, Content: functionResultContent(text), DeliveryID: delivery})
		if err := router.Handle(t.Context(), env); err != nil {
			t.Fatal(err)
		}
		frames := sender.snapshot()
		last := frames[len(frames)-1]
		var ack proto.InteractionDecisionAckPayload
		if err := last.DecodePayload(&ack); err != nil || last.Type != proto.TypeInteractionDecisionAck || last.ID != run || ack.DeliveryID != delivery {
			t.Fatal(last, err)
		}
		return ack
	}
	if a := submit("missing", "call", "answer", "a"); a.Applied || a.ErrorCode != "not_pending" {
		t.Fatal(a)
	}
	if a := submit("one", "missing", "answer", "b"); a.Applied || a.ErrorCode != "not_pending" {
		t.Fatal(a)
	}

	invalid := scoped(t, "one", proto.TypeFunctionResult, "one", proto.FunctionResultPayload{CallID: "call", DeliveryID: "invalid", Content: []proto.InputContent{{Type: "input_audio"}}})
	if err := router.Handle(t.Context(), invalid); err != nil {
		t.Fatal(err)
	}
	frames := sender.snapshot()
	var invalidAck proto.InteractionDecisionAckPayload
	_ = frames[len(frames)-1].DecodePayload(&invalidAck)
	if invalidAck.Applied || invalidAck.ErrorCode != "invalid_result" {
		t.Fatal(invalidAck)
	}
	// A lost receipt may be retried without writing the native result twice.
	sender.mu.Lock()
	sender.failNow = true
	sender.mu.Unlock()
	first := scoped(t, "one", proto.TypeFunctionResult, "one", proto.FunctionResultPayload{CallID: "call", Success: true, Content: functionResultContent("answer"), DeliveryID: "lost"})
	if err := router.Handle(t.Context(), first); err == nil {
		t.Fatal("receipt send failure was hidden")
	}
	if a := submit("one", "call", "answer", "retry"); !a.Applied {
		t.Fatal(a)
	}
	if a := submit("one", "call", "changed", "conflict"); a.Applied || a.ErrorCode != "decision_conflict" {
		t.Fatal(a)
	}
	// The cached receipt answers only the assignment that applied the result.
	foreign := scoped(t, "one", proto.TypeFunctionResult, "one", proto.FunctionResultPayload{CallID: "call", Success: true, Content: functionResultContent("answer"), DeliveryID: "foreign"})
	foreign.Assignment.AssignmentID = "foreign"
	if err := router.Handle(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	frames = sender.snapshot()
	var foreignAck proto.InteractionDecisionAckPayload
	if last := frames[len(frames)-1]; last.DecodePayload(&foreignAck) != nil || foreignAck.Applied || foreignAck.ErrorCode != proto.AssignmentConflict || last.Assignment != foreign.Assignment {
		t.Fatal(last, foreignAck)
	}

	for _, mutation := range []string{"image", "order", "success"} {
		result := proto.FunctionResultPayload{CallID: "call", Success: true, Content: functionResultContent("answer"), DeliveryID: mutation}
		switch mutation {
		case "image":
			other := "https://example.com/other.png"
			result.Content[1].ImageURL = &other
		case "order":
			result.Content[0], result.Content[1] = result.Content[1], result.Content[0]
		case "success":
			result.Success = false
		}
		env := scoped(t, "one", proto.TypeFunctionResult, "one", result)
		if err := router.Handle(t.Context(), env); err != nil {
			t.Fatal(err)
		}
		frames := sender.snapshot()
		var ack proto.InteractionDecisionAckPayload
		_ = frames[len(frames)-1].DecodePayload(&ack)
		if ack.Applied || ack.ErrorCode != "decision_conflict" {
			t.Fatal(mutation, ack)
		}
	}
	if a := submit("two", "call", "second answer", "other-run"); !a.Applied {
		t.Fatal(a)
	}
	for _, s := range sessions {
		s.mu.Lock()
		count := s.calls
		s.mu.Unlock()
		if count != 1 {
			t.Fatal(count)
		}
	}
}

// rejectsBeforeFactory reports whether a Router rejects preparing req, whose
// kind declares caps, as an unsupported configuration before the kind's
// factory.
func rejectsBeforeFactory(t *testing.T, caps proto.AgentKindCapabilities, req proto.PromptRequestPayload) bool {
	t.Helper()
	reg := agent.NewRegistry()
	var called atomic.Bool
	registerExecutorKind(reg, proto.SupportedAgentKind{Kind: req.AgentKind, Available: true, Capabilities: caps}, func(context.Context, agent.PrepareRequest) (agent.Executor, error) {
		called.Store(true)
		return nil, errors.New("unexpected executor preparation")
	})
	sender := &recSender{}
	router, _ := dispatch.New(dispatch.Config{Registry: reg, Sender: sender})
	defer router.Shutdown(context.Background())
	assign(t, router, preparationSessionID, "")
	err := router.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "prepare", noEnvironmentPreparation(preparationSessionID, req)))
	status := waitPreparationStatus(t, sender, "prepare", "rejected", "")
	return err != nil && status.ErrorCode == "unsupported_configuration" && !called.Load()
}

func TestFunctionToolsRequireAdvertisedSupport(t *testing.T) {
	caps := prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported})
	if !rejectsBeforeFactory(t, caps, proto.PromptRequestPayload{AgentKind: "unsupported", FunctionTools: []proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{}`)}}}) {
		t.Fatal("unsupported engine silently ignored tools")
	}
}

func functionResultContent(text string) []proto.InputContent {
	picture := image.NewRGBA(image.Rect(0, 0, 1, 1))
	picture.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		panic(err)
	}
	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	after := "AFTER-IMAGE"
	return []proto.InputContent{{Type: "input_text", Text: &text}, {Type: "input_image", ImageURL: &imageURL}, {Type: "input_text", Text: &after}}
}

func TestDiscoveryCannotReachAnEagerOnlyAdapter(t *testing.T) {
	caps := prototest.Capabilities(proto.AgentKindCapabilities{EnvironmentNone: proto.CapabilitySupported, FunctionTools: proto.CapabilitySupported})
	for _, search := range []bool{false, true} {
		if !rejectsBeforeFactory(t, caps, proto.PromptRequestPayload{AgentKind: "eager-only", ToolSearch: search, FunctionTools: []proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`), DeferLoading: true}}}) {
			t.Fatal("deferred definitions reached an eager-only adapter")
		}
	}
}
