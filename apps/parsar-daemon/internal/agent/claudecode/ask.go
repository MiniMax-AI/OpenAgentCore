package claudecode

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// askUserQuestionToolName matches Claude Code's built-in tool name.
const askUserQuestionToolName = "AskUserQuestion"

// pendingAskTable maps daemon-minted ask IDs to control requests and their
// question snapshots. Take lets only one answer or timeout consume an ask.
type pendingAskTable struct {
	mu      sync.Mutex
	byAskID map[string]pendingAskEntry
}

type pendingAskEntry struct {
	CCRequestID string
	Questions   []proto.PromptForUserChoiceQuestion
}

func newPendingAskTable() *pendingAskTable {
	return &pendingAskTable{byAskID: make(map[string]pendingAskEntry)}
}

// RecordControl links a freshly minted ask id to the originating CC
// request_id (control_request path). Used when claude-code wraps
// AskUserQuestion as a can_use_tool permission check.
func (p *pendingAskTable) RecordControl(askID, ccRequestID string, questions []proto.PromptForUserChoiceQuestion) {
	if askID == "" || ccRequestID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byAskID[askID] = pendingAskEntry{CCRequestID: ccRequestID, Questions: questions}
}

// Take atomically reads + deletes the entry recorded for askID. Used by
// SubmitPromptForUserChoice so two near-simultaneous callers (timer-
// fired cancel racing a server-delivered answer) can't both pass and
// each write a control_response. The loser sees ok=false and returns
// ErrUnknownAsk instead.
//
// Trade-off vs Resolve+Delete: if the subsequent stdin write fails, the
// entry is already gone — a retry surfaces ErrUnknownAsk rather than
// re-doing the write. The double-fire risk is the bigger hazard here
// (timer + server can both reach Submit; stdin flakes are rare and the
// session is going to die anyway when stdin errors), so we accept it.
func (p *pendingAskTable) Take(askID string, decision proto.PromptForUserChoiceDecisionPayload) (pendingAskEntry, bool, error) {
	if askID == "" {
		return pendingAskEntry{}, false, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.byAskID[askID]
	if !ok {
		return pendingAskEntry{}, false, nil
	}
	if _, err := decision.AnswersFor(askQuestionIDs(e)); err != nil {
		return pendingAskEntry{}, false, err
	}
	delete(p.byAskID, askID)
	return e, true, nil
}

// interceptAskUserQuestionFromControlRequest handles the can_use_tool check
// emitted under --permission-prompt-tool stdio. The assistant tool_use copy
// remains a normal tool event; only this request creates an ask.
func (t *translator) interceptAskUserQuestionFromControlRequest(ccRequestID string, input map[string]any) (proto.Envelope, bool) {
	if t.askPending == nil || t.askMint == nil {
		return proto.Envelope{}, false
	}
	if ccRequestID == "" {
		return proto.Envelope{}, false
	}
	questions, ok := parseAskUserQuestionInput(input)
	if !ok {
		return proto.Envelope{}, false
	}

	askID := t.askMint()
	t.askPending.RecordControl(askID, ccRequestID, questions)

	env, err := proto.NewEnvelope(proto.TypePromptForUserChoice, t.runID, proto.PromptForUserChoicePayload{
		AskID:     askID,
		Questions: questions,
	})
	if err != nil {
		return proto.Envelope{}, false
	}
	return env, true
}

// parseAskUserQuestionInput pulls the AskUserQuestion fields out of
// the raw tool input. The schema mirrors Claude Code's built-in:
//
//	{
//	  "questions": [{
//	    "header":  "...",
//	    "question": "...",
//	    "multiSelect": false,
//	    "options": [{"label": "...", "description": "..."}, ...]
//	  }, ...]
//	}
//
// Returns ok=false (fall through to a normal TypeToolCall) when the
// shape doesn't fit. Any single question with an empty question text
// or zero options invalidates the whole call — we'd rather let claude
// see the raw tool_use and re-emit than render a half-broken card.
func parseAskUserQuestionInput(input map[string]any) ([]proto.PromptForUserChoiceQuestion, bool) {
	rawQuestions, exists := input["questions"]
	if !exists {
		return nil, false
	}
	list, isList := rawQuestions.([]any)
	if !isList || len(list) == 0 {
		return nil, false
	}

	out := make([]proto.PromptForUserChoiceQuestion, 0, len(list))
	for index, rawEntry := range list {
		q, isMap := rawEntry.(map[string]any)
		if !isMap {
			return nil, false
		}
		question, _ := q["question"].(string)
		header, _ := q["header"].(string)
		multiSelect, _ := q["multiSelect"].(bool)

		rawOptions, _ := q["options"].([]any)
		options := make([]proto.PromptForUserChoiceOption, 0, len(rawOptions))
		for _, raw := range rawOptions {
			om, isOptMap := raw.(map[string]any)
			if !isOptMap {
				continue
			}
			label, _ := om["label"].(string)
			if label == "" {
				continue
			}
			description, _ := om["description"].(string)
			options = append(options, proto.PromptForUserChoiceOption{
				Label:       label,
				Description: description,
			})
		}
		if question == "" || len(options) == 0 {
			return nil, false
		}
		out = append(out, proto.PromptForUserChoiceQuestion{
			ID:          fmt.Sprintf("q%d", index),
			Header:      header,
			Question:    question,
			MultiSelect: multiSelect,
			// Claude Code's AskUserQuestion always permits the built-in
			// "Other" free-text answer in addition to declared options.
			IsOther: true,
			Options: options,
		})
	}
	return out, true
}

// buildAskUserControlResponse answers the native permission check. Denying
// execution skips Claude's local AskUserQuestion handler; the SDK supplies the
// message to the model as the tool result, including cancellation instructions.
func buildAskUserControlResponse(entry pendingAskEntry, decision proto.PromptForUserChoiceDecisionPayload) ([]byte, error) {
	answers, err := decision.AnswersFor(askQuestionIDs(entry))
	if err != nil {
		return nil, err
	}
	text := formatAskUserResultText(entry, decision, answers)
	body, err := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": entry.CCRequestID,
			"response": map[string]any{
				"behavior": "deny",
				"message":  text,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// formatAskUserResultText formats answers or cancellation instructions for Claude.
func formatAskUserResultText(entry pendingAskEntry, decision proto.PromptForUserChoiceDecisionPayload, answers map[string][]string) string {
	if decision.Cancelled {
		reason := strings.TrimSpace(decision.Reason)
		switch reason {
		case "timeout":
			return "The user did not make a selection within 10 minutes. Stop the current operation, report the timeout to the user and ask about follow-up intent; do not retry this tool."
		case "cancelled":
			return "The user cancelled this operation. Stop follow-up actions."
		default:
			return "The user did not give a selection (" + reason + "). Stop follow-up actions and wait for further instructions from the user."
		}
	}

	out := make([]map[string]any, 0, len(entry.Questions))
	anyAnswer := false
	for _, q := range entry.Questions {
		answer := strings.Join(answers[q.ID], "、")
		if answer != "" {
			anyAnswer = true
		}
		out = append(out, map[string]any{
			"header": q.Header,
			"answer": answer,
		})
	}
	if !anyAnswer {
		// Treat as cancel; the operator effectively chose nothing.
		return "The user did not choose any option. Stop follow-up actions and wait for further instructions from the user."
	}
	return mustMarshalAskQuestions(out)
}

func mustMarshalAskQuestions(qs []map[string]any) string {
	payload, _ := json.Marshal(map[string]any{"questions": qs})
	return string(payload)
}

func askQuestionIDs(entry pendingAskEntry) []string {
	ids := make([]string, len(entry.Questions))
	for i, question := range entry.Questions {
		ids[i] = question.ID
	}
	return ids
}
