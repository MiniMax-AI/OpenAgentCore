package runtimegateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// PrepareRuntime sends one typed preparation operation once. An
// interrupted or unconfirmed transfer reports unknown; callers must not replay it.
func (s *Session) PrepareRuntime(ctx context.Context, id string, request proto.RuntimePreparePayload, data []byte) (proto.RuntimePrepareResultPayload, error) {
	unknown := proto.RuntimePrepareResultPayload{Outcome: "unknown", ErrorCode: "runtime_preparation_unconfirmed"}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id || len(data) > proto.RuntimePrepareMaxBytes ||
		(request.Step != "" && request.Step != "begin") {
		return unknown, errors.New("agentdaemon gateway: invalid Runtime preparation")
	}
	request.Step = "begin"
	if request.Action == "finalize" || request.Action == "initialize" {
		if len(data) != 0 {
			return unknown, errors.New("agentdaemon gateway: invalid Runtime finalization")
		}
	} else {
		digest := sha256.Sum256(data)
		expected := hex.EncodeToString(digest[:])
		if (request.SizeBytes != 0 && request.SizeBytes != len(data)) || (request.SHA256 != "" && request.SHA256 != expected) {
			return unknown, errors.New("agentdaemon gateway: Runtime archive mismatch")
		}
		request.SizeBytes, request.SHA256 = len(data), expected
	}
	if !proto.ValidRuntimePrepareRequest(request) {
		return unknown, errors.New("agentdaemon gateway: invalid Runtime preparation")
	}
	s.capabilitiesMu.Lock()
	if s.IsClosed() {
		s.capabilitiesMu.Unlock()
		return unknown, ErrSessionClosed
	}
	if len(s.capabilities) != 0 {
		s.capabilitiesMu.Unlock()
		return unknown, errors.New("agentdaemon gateway: Runtime preparation capacity")
	}
	replies := make(chan proto.Envelope, 1)
	s.capabilities = map[string]chan proto.Envelope{id: replies}
	s.capabilitiesMu.Unlock()
	defer func() { s.capabilitiesMu.Lock(); delete(s.capabilities, id); s.capabilitiesMu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 195*time.Second)
	defer cancel()
	exchange := func(payload proto.RuntimePreparePayload, outcome string, offset int) (proto.RuntimePrepareResultPayload, error) {
		env, err := proto.NewEnvelope(proto.TypeRuntimePrepare, id, payload)
		if err != nil {
			return unknown, errors.New("agentdaemon gateway: invalid Runtime frame")
		}
		encoded, err := json.Marshal(env)
		if err != nil || len(encoded) > proto.RuntimePrepareMaxFrameBytes {
			return unknown, errors.New("agentdaemon gateway: invalid Runtime frame")
		}
		reply, err := s.exchangeChunkFrame(ctx, env, replies)
		if err != nil {
			return unknown, err
		}
		var result proto.RuntimePrepareResultPayload
		if len(reply.Payload) > proto.RuntimePrepareMaxFrameBytes || reply.DecodePayload(&result) != nil ||
			!proto.ValidRuntimePrepareResult(result, outcome, offset, len(data)) {
			return unknown, errors.New("agentdaemon gateway: invalid Runtime receipt")
		}
		if result.Outcome == "unknown" {
			return result, errors.New("agentdaemon gateway: Runtime preparation unconfirmed")
		}
		return result, nil
	}
	result, err := exchange(request, "ready", 0)
	if err != nil || result.Outcome != "ready" {
		return result, err
	}
	for offset := 0; offset < len(data); {
		end := min(offset+proto.RuntimePrepareChunkBytes, len(data))
		result, err = exchange(proto.RuntimePreparePayload{Step: "chunk", Offset: offset, Data: data[offset:end]}, "received", end)
		if err != nil || result.Outcome != "received" {
			return result, err
		}
		offset = end
	}
	return exchange(proto.RuntimePreparePayload{Step: "commit"}, "completed", 0)
}

func (s *Session) dispatchCapabilities(env proto.Envelope) {
	s.capabilitiesMu.Lock()
	defer s.capabilitiesMu.Unlock()
	if replies := s.capabilities[env.ID]; replies != nil {
		select {
		case replies <- env:
		default:
		}
	}
}

func (s *Session) closeCapabilities() {
	s.capabilitiesMu.Lock()
	defer s.capabilitiesMu.Unlock()
	for id, replies := range s.capabilities {
		close(replies)
		delete(s.capabilities, id)
	}
}
