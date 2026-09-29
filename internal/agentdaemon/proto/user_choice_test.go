package proto

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUserChoiceOnlyAcceptsCurrentAnswerShape(t *testing.T) {
	for _, raw := range []string{
		`null`,
		`{"answers":["yes"]}`,
		`{"question_answers":[{"header":"Question","answer":"yes"}]}`,
		`{"question_answers":[{"question_id":"q","answers":["yes"],"answer":"no"}]}`,
		`{"question_answers":[{"answers":["yes"]}]}`,
		`{"question_answers":[{"question_id":"q","answers":null}]}`,
		`{"question_answers":[{"question_id":"q","answers":["yes"]},{"question_id":"q","answers":["no"]}]}`,
		`{"cancelled":true,"question_answers":[{"question_id":"q","answers":["yes"]}]}`,
	} {
		var decision PromptForUserChoiceDecisionPayload
		if json.Unmarshal([]byte(raw), &decision) == nil {
			t.Fatalf("accepted invalid decision: %s", raw)
		}
	}
	var decision PromptForUserChoiceDecisionPayload
	if err := json.Unmarshal([]byte(`{"delivery_id":"d","question_answers":[{"question_id":"b","answers":["two,three","four"]},{"question_id":"a","answers":[]}]}`), &decision); err != nil {
		t.Fatal(err)
	}
	answers, err := decision.AnswersFor([]string{"a", "b"})
	if err != nil || !reflect.DeepEqual(answers["b"], []string{"two,three", "four"}) || answers["a"] == nil {
		t.Fatalf("answers=%v err=%v", answers, err)
	}
	if _, err := decision.AnswersFor([]string{"a"}); err == nil {
		t.Fatal("accepted a foreign question ID")
	}
	if _, err := (PromptForUserChoiceDecisionPayload{Cancelled: true}).AnswersFor([]string{"a"}); err != nil {
		t.Fatal(err)
	}
}

func TestToolCallRejectsEngineSnapshot(t *testing.T) {
	var call ToolCallPayload
	if json.Unmarshal([]byte(`{"id":"tool","stage":"after","native_item":{"type":"commandExecution"}}`), &call) == nil {
		t.Fatal("accepted engine-specific snapshot in common wire")
	}
}
