package codex

import (
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (s *Session) beginUsageTurn(turnID string) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if s.usageTurnID == turnID {
		return
	}
	s.usageTurnID = turnID
	s.usageBaseline = s.usageTotal
	s.latestUsage = nil
}

func (s *Session) onUsageUpdated(raw json.RawMessage) {
	var p ThreadTokenUsageUpdatedNotification
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	if !s.isRootThread(p.ThreadID) {
		s.usageMu.Lock()
		if s.resumeUsageThreadID != "" && p.ThreadID == s.resumeUsageThreadID && p.TurnID != "" && p.TokenUsage != nil && p.TokenUsage.Total != nil {
			s.resumeUsageTotal = p.TokenUsage.Total
		}
		s.usageMu.Unlock()
		return
	}
	if s.terminal.Load() {
		return
	}
	var observed *TurnUsage
	s.usageMu.Lock()
	defer func() {
		s.usageMu.Unlock()
		// Native notifications are ordered by the RPC reader. Publish before it
		// reads completion, without holding the snapshot lock across backpressure.
		if observed != nil {
			s.emitUsage(*observed)
		}
	}()
	if p.TokenUsage != nil && p.TokenUsage.Total != nil && p.TurnID != "" {
		// app-server replays the previous thread total after resume and
		// before turn/started. It establishes a baseline, not new usage.
		if s.usageTurnID == "" {
			s.usageTotal = *p.TokenUsage.Total
			return
		}
		if p.TurnID != s.usageTurnID {
			return
		}
		s.usageTotal = *p.TokenUsage.Total
		u := subtractUsage(s.usageTotal, s.usageBaseline)
		if u.InputTokens == 0 && u.OutputTokens == 0 && u.TotalTokens == 0 {
			// The thread total has not advanced past this Turn's baseline. It
			// reports no usage for this Turn, e.g. when native sends it after an
			// interrupt before any response reported usage; publishing the
			// difference would turn missing usage into measured zeros.
			return
		}
		observed = &u
	} else if p.Usage != nil && s.usageTurnID != "" && (p.TurnID == "" || p.TurnID == s.usageTurnID) {
		u := *p.Usage
		observed = &u
	}
	if observed != nil {
		s.latestUsage = observed
	}
}

func subtractUsage(total, baseline TurnUsage) TurnUsage {
	return TurnUsage{
		observed: true,
		complete: total.completeTokens() && (!baseline.observed || baseline.completeTokens()) &&
			total.InputTokens >= baseline.InputTokens && total.OutputTokens >= baseline.OutputTokens &&
			total.CachedInputTokens >= baseline.CachedInputTokens &&
			total.ReasoningOutputTokens >= baseline.ReasoningOutputTokens && total.TotalTokens >= baseline.TotalTokens,
		ReasoningOutputTokens: max(0, total.ReasoningOutputTokens-baseline.ReasoningOutputTokens),
		InputTokens:           max(0, total.InputTokens-baseline.InputTokens),
		OutputTokens:          max(0, total.OutputTokens-baseline.OutputTokens),
		CachedInputTokens:     max(0, total.CachedInputTokens-baseline.CachedInputTokens),
		CacheReadInputTokens:  max(0, total.CacheReadInputTokens-baseline.CacheReadInputTokens),
		TotalTokens:           max(0, total.TotalTokens-baseline.TotalTokens),
	}
}

func (u *TurnUsage) UnmarshalJSON(raw []byte) error {
	type plain TurnUsage
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*u = TurnUsage(value)
	u.observed, u.complete = true, true
	for _, key := range []string{"inputTokens", "outputTokens", "cachedInputTokens", "reasoningOutputTokens", "totalTokens"} {
		var n *int64
		if json.Unmarshal(fields[key], &n) != nil || n == nil || *n < 0 {
			u.complete = false
		}
	}
	return nil
}

func (u TurnUsage) completeTokens() bool {
	return u.complete && u.CachedInputTokens <= u.InputTokens && u.ReasoningOutputTokens <= u.OutputTokens &&
		u.InputTokens <= u.TotalTokens && u.OutputTokens == u.TotalTokens-u.InputTokens
}

func (s *Session) usagePayload(u TurnUsage) proto.Usage {
	result := proto.Usage{Provider: "openai", Model: s.resolvedModel, InputTokens: int32(u.InputTokens), OutputTokens: int32(u.OutputTokens)}
	if u.completeTokens() {
		result.Tokens = &proto.TokenUsage{InputTokens: int64(u.InputTokens), OutputTokens: int64(u.OutputTokens), CachedInputTokens: int64(u.CachedInputTokens), ReasoningOutputTokens: int64(u.ReasoningOutputTokens), TotalTokens: int64(u.TotalTokens)}
	}
	return result
}
