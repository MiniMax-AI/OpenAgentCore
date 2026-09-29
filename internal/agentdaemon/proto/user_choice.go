package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// UnmarshalJSON rejects fields outside the current decision contract, including
// unrecognized fields nested inside question_answers. Do not silently discard an
// answer from a peer using a different wire shape.
func (p *PromptForUserChoiceDecisionPayload) UnmarshalJSON(raw []byte) error {
	if raw = bytes.TrimSpace(raw); len(raw) == 0 || raw[0] != '{' {
		return errors.New("user-choice decision must be an object")
	}
	type decision PromptForUserChoiceDecisionPayload
	var value decision
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return errors.New("invalid user-choice decision")
	}
	decoded := PromptForUserChoiceDecisionPayload(value)
	if err := decoded.Validate(); err != nil {
		return err
	}
	*p = decoded
	return nil
}

// Validate checks answer identity independently of delivery correlation and
// native question constraints. An empty answer array is an explicit non-answer.
func (p PromptForUserChoiceDecisionPayload) Validate() error {
	if p.Cancelled && len(p.QuestionAnswers) != 0 {
		return errors.New("cancelled user-choice decision cannot contain answers")
	}
	seen := make(map[string]bool, len(p.QuestionAnswers))
	for _, answer := range p.QuestionAnswers {
		if strings.TrimSpace(answer.QuestionID) == "" || seen[answer.QuestionID] || answer.Answers == nil {
			return errors.New("user-choice answers require unique question IDs and answer arrays")
		}
		seen[answer.QuestionID] = true
	}
	return nil
}

// AnswersFor binds a decision to the exact emitted question identities. It never
// derives identity from a header, answer text or array position. Call it before
// consuming the pending interaction or submitting a native response.
func (p PromptForUserChoiceDecisionPayload) AnswersFor(questionIDs []string) (map[string][]string, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(questionIDs))
	for _, id := range questionIDs {
		known[id] = true
	}
	answers := make(map[string][]string, len(p.QuestionAnswers))
	for _, answer := range p.QuestionAnswers {
		if !known[answer.QuestionID] {
			return nil, errors.New("user-choice answer refers to an unknown question")
		}
		answers[answer.QuestionID] = answer.Answers
	}
	return answers, nil
}
