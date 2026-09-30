package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/google/uuid"
)

func skillPreparation() proto.RuntimePreparePayload {
	return proto.RuntimePreparePayload{EnvironmentID: uuid.NewString(), SessionID: uuid.NewString(), Action: "skill",
		Skill: &agentskill.Metadata{Type: "inline", Name: "example", Description: "Example"}}
}

type capabilityOutcome struct {
	result proto.RuntimePrepareResultPayload
	err    error
}

func beginCapabilities(s *Session, ctx context.Context, id string, request proto.RuntimePreparePayload, data []byte) <-chan capabilityOutcome {
	done := make(chan capabilityOutcome, 1)
	go func() { r, err := s.PrepareRuntime(ctx, id, request, data); done <- capabilityOutcome{r, err} }()
	return done
}
func nextCapabilityFrame(t *testing.T, s *Session) proto.Envelope {
	t.Helper()
	select {
	case env := <-s.sendCh:
		return env
	case <-time.After(3 * time.Second):
		t.Fatal("transfer stopped unexpectedly")
		return proto.Envelope{}
	}
}
func finishCapabilities(t *testing.T, done <-chan capabilityOutcome) capabilityOutcome {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("transfer did not settle")
		return capabilityOutcome{}
	}
}
func replyCapabilities(s *Session, id string, result proto.RuntimePrepareResultPayload) {
	reply, _ := proto.NewEnvelope(proto.TypeRuntimePrepareResult, id, result)
	s.dispatch(reply)
}
func noCapabilityFrame(t *testing.T, s *Session) {
	t.Helper()
	select {
	case env := <-s.sendCh:
		t.Fatal("unexpected extra frame", env.Type)
	default:
	}
}

func TestCapabilitiesTransfersMoreThanFrameLimitAndCorrelates(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	data := bytes.Repeat([]byte{0, 255, 3}, 2<<20)
	id := uuid.NewString()
	request := skillPreparation()
	done := beginCapabilities(s, t.Context(), id, request, data)
	var received []byte
	for {
		env := nextCapabilityFrame(t, s)
		encoded, err := json.Marshal(env)
		var p proto.RuntimePreparePayload
		if err != nil || len(encoded) > proto.RuntimePrepareMaxFrameBytes || env.ID != id || env.Type != proto.TypeRuntimePrepare || env.DecodePayload(&p) != nil || !proto.ValidRuntimePrepareRequest(p) {
			t.Fatal("invalid frame")
		}
		result := proto.RuntimePrepareResultPayload{}
		switch p.Step {
		case "begin":
			digest := sha256.Sum256(data)
			if p.EnvironmentID != request.EnvironmentID || p.SessionID != request.SessionID || p.Skill == nil || *p.Skill != *request.Skill || p.SizeBytes != len(data) || p.SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatal("identity, metadata or digest changed")
			}
			result.Outcome = "ready"
		case "chunk":
			if p.Offset != len(received) {
				t.Fatal("noncontiguous chunks")
			}
			received = append(received, p.Data...)
			result.Outcome, result.Offset = "received", len(received)
		case "commit":
			if !bytes.Equal(received, data) {
				t.Fatal("archive changed")
			}
			result.Outcome, result.SizeBytes = "completed", len(data)
		}
		replyCapabilities(s, uuid.NewString(), result)
		replyCapabilities(s, id, result)
		if p.Step == "commit" {
			break
		}
	}
	result := finishCapabilities(t, done)
	if result.err != nil || result.result.Outcome != "completed" || result.result.SizeBytes != len(data) {
		t.Fatal(result)
	}
	noCapabilityFrame(t, s)
}

func TestCapabilitiesFinalizeTransfersNoArchive(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	request := proto.RuntimePreparePayload{EnvironmentID: uuid.NewString(), SessionID: uuid.NewString(), Action: "finalize", Sources: &agentcapabilities.Input{}}
	id := uuid.NewString()
	done := beginCapabilities(s, t.Context(), id, request, nil)
	env := nextCapabilityFrame(t, s)
	var p proto.RuntimePreparePayload
	if env.DecodePayload(&p) != nil || p.Step != "begin" || p.Sources == nil || p.SizeBytes != 0 || p.SHA256 != "" {
		t.Fatal("invalid finalization", p)
	}
	replyCapabilities(s, id, proto.RuntimePrepareResultPayload{Outcome: "ready"})
	env = nextCapabilityFrame(t, s)
	if env.DecodePayload(&p) != nil || p.Step != "commit" {
		t.Fatal("finalization sent archive")
	}
	replyCapabilities(s, id, proto.RuntimePrepareResultPayload{Outcome: "completed"})
	if result := finishCapabilities(t, done); result.err != nil || result.result.Outcome != "completed" {
		t.Fatal(result)
	}
}

func TestCapabilitiesRefusesConflictingArchiveBeforeSending(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	for _, mutate := range []func(*proto.RuntimePreparePayload){
		func(p *proto.RuntimePreparePayload) { p.SHA256 = "incorrect" },
		func(p *proto.RuntimePreparePayload) { p.SizeBytes = 999 },
		func(p *proto.RuntimePreparePayload) { p.Sources = &agentcapabilities.Input{} },
	} {
		request := skillPreparation()
		mutate(&request)
		if result, err := s.PrepareRuntime(t.Context(), uuid.NewString(), request, []byte("data")); err == nil || result.Outcome != "unknown" {
			t.Fatal("conflict admitted", result, err)
		}
		noCapabilityFrame(t, s)
	}
}

func TestCapabilitiesStopsAtTerminalOrMalformedReceipt(t *testing.T) {
	for name, receipt := range map[string]proto.RuntimePrepareResultPayload{
		"rejected":            {Outcome: "rejected", ErrorCode: "runtime_preparation_rejected"},
		"failed":              {Outcome: "failed", ErrorCode: "runtime_preparation_failed"},
		"unknown":             {Outcome: "unknown", ErrorCode: "runtime_preparation_unconfirmed"},
		"premature completed": {Outcome: "completed", SizeBytes: 4},
		"wrong offset":        {Outcome: "ready", Offset: 1},
		"unsafe code":         {Outcome: "rejected", ErrorCode: "private detail"},
	} {
		t.Run(name, func(t *testing.T) {
			s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
			defer s.Close("test")
			id := uuid.NewString()
			done := beginCapabilities(s, t.Context(), id, skillPreparation(), []byte("data"))
			nextCapabilityFrame(t, s)
			replyCapabilities(s, id, receipt)
			result := finishCapabilities(t, done)
			if name == "rejected" || name == "failed" {
				if result.err != nil || result.result != receipt {
					t.Fatal(result)
				}
			} else if result.err == nil || result.result.Outcome != "unknown" {
				t.Fatal("uncertainty lost", result)
			}
			noCapabilityFrame(t, s)
		})
	}
}

func TestCapabilitiesRejectsWrongChunkReceipt(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	id := uuid.NewString()
	done := beginCapabilities(s, t.Context(), id, skillPreparation(), []byte("data"))
	nextCapabilityFrame(t, s)
	replyCapabilities(s, id, proto.RuntimePrepareResultPayload{Outcome: "ready"})
	nextCapabilityFrame(t, s)
	replyCapabilities(s, id, proto.RuntimePrepareResultPayload{Outcome: "received", Offset: 3})
	result := finishCapabilities(t, done)
	if result.err == nil || result.result.Outcome != "unknown" {
		t.Fatal(result)
	}
	noCapabilityFrame(t, s)
}

func TestCapabilitiesConnectionOwnershipAndUnknownInterruption(t *testing.T) {
	for _, closeConnection := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "disconnect"}[closeConnection], func(t *testing.T) {
			s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
			defer s.Close("test")
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			done := beginCapabilities(s, ctx, uuid.NewString(), skillPreparation(), []byte("data"))
			nextCapabilityFrame(t, s)
			if _, err := s.PrepareRuntime(t.Context(), uuid.NewString(), skillPreparation(), []byte("second")); err == nil {
				t.Fatal("concurrent transfer admitted")
			}
			if closeConnection {
				s.Close("lost connection")
			}
			result := finishCapabilities(t, done)
			if result.result.Outcome != "unknown" || result.result.ErrorCode != "runtime_preparation_unconfirmed" {
				t.Fatal("interruption claimed rejection", result)
			}
			expected := error(context.DeadlineExceeded)
			if closeConnection {
				expected = ErrSessionClosed
			}
			if !errors.Is(result.err, expected) {
				t.Fatal(result.err)
			}
			noCapabilityFrame(t, s)
			s.capabilitiesMu.Lock()
			remaining := len(s.capabilities)
			s.capabilitiesMu.Unlock()
			if remaining != 0 {
				t.Fatal("transfer ownership retained")
			}
		})
	}
}

func TestRuntimeInitialFileChunking(t *testing.T) {
	for _, size := range []int{0, (4 << 20) + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
			defer s.Close("test")
			request := proto.RuntimePreparePayload{EnvironmentID: uuid.NewString(), SessionID: uuid.NewString(), Action: "file", File: &proto.RuntimeInitialFile{Path: "/workspace/project/file"}}
			data := bytes.Repeat([]byte("z"), size)
			id := uuid.NewString()
			done := beginCapabilities(s, t.Context(), id, request, data)
			var received []byte
			for {
				env := nextCapabilityFrame(t, s)
				var p proto.RuntimePreparePayload
				if env.DecodePayload(&p) != nil || !proto.ValidRuntimePrepareRequest(p) {
					t.Fatal("invalid file frame")
				}
				result := proto.RuntimePrepareResultPayload{}
				switch p.Step {
				case "begin":
					sum := sha256.Sum256(data)
					if p.File == nil || p.File.Path != request.File.Path || p.SizeBytes != size || p.SHA256 != hex.EncodeToString(sum[:]) {
						t.Fatal("file header changed")
					}
					result.Outcome = "ready"
				case "chunk":
					if p.Offset != len(received) {
						t.Fatal("wrong chunk offset")
					}
					received = append(received, p.Data...)
					result.Outcome, result.Offset = "received", len(received)
				case "commit":
					if !bytes.Equal(data, received) {
						t.Fatal("file bytes changed")
					}
					result.Outcome, result.SizeBytes = "completed", size
				}
				replyCapabilities(s, id, result)
				if p.Step == "commit" {
					break
				}
			}
			got := finishCapabilities(t, done)
			if got.err != nil || got.result.Outcome != "completed" {
				t.Fatal(got)
			}
			noCapabilityFrame(t, s)
		})
	}
}

func TestRuntimeInitializationNoDataAndExitReceipt(t *testing.T) {
	for _, exit := range []int{0, 42, 256} {
		t.Run(fmt.Sprint(exit), func(t *testing.T) {
			s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
			defer s.Close("test")
			request := proto.RuntimePreparePayload{EnvironmentID: uuid.NewString(), SessionID: uuid.NewString(), Action: "initialize", Initialization: &proto.RuntimeInitialization{Action: "setup", Network: "enabled", Command: "echo test"}}
			if _, err := s.PrepareRuntime(t.Context(), uuid.NewString(), request, []byte("forbidden")); err == nil {
				t.Fatal("initialization body accepted")
			}
			noCapabilityFrame(t, s)
			id := uuid.NewString()
			done := beginCapabilities(s, t.Context(), id, request, nil)
			env := nextCapabilityFrame(t, s)
			var p proto.RuntimePreparePayload
			if env.DecodePayload(&p) != nil {
				t.Fatal("invalid initialization frame")
			}
			if p.Step != "begin" || p.Initialization == nil || p.Initialization.Command != request.Initialization.Command || p.SizeBytes != 0 || p.SHA256 != "" {
				t.Fatal("initialization changed")
			}
			replyCapabilities(s, id, proto.RuntimePrepareResultPayload{Outcome: "ready"})
			env = nextCapabilityFrame(t, s)
			p = proto.RuntimePreparePayload{}
			if env.DecodePayload(&p) != nil || p.Step != "commit" {
				t.Fatal("initialization sent data")
			}
			result := proto.RuntimePrepareResultPayload{Outcome: "completed"}
			if exit != 0 {
				result = proto.RuntimePrepareResultPayload{Outcome: "failed", ErrorCode: "runtime_preparation_failed", ExitCode: exit}
			}
			replyCapabilities(s, id, result)
			got := finishCapabilities(t, done)
			if exit == 256 {
				if got.err == nil || got.result.Outcome != "unknown" {
					t.Fatal("unsafe exit accepted", got)
				}
			} else if got.err != nil || got.result != result {
				t.Fatal(got)
			}
			noCapabilityFrame(t, s)
		})
	}
}
