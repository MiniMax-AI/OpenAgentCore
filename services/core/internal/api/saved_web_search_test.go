package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TV-05 W1/W2: saved web_search keeps every pinned mode with the official
// projection, and Session admission resolves it to the same bytes.
func TestSavedWebSearchProjection(t *testing.T) {
	const defaults = `"context_size":"medium","allowed_domains":null,"location":null}`
	live := `{"type":"web_search","mode":"live",` + defaults
	for _, tc := range []struct{ input, want string }{
		{`{"type":"web_search"}`, live},
		{`{"type":"web_search","mode":null}`, live},
		{`{"type":"web_search","mode":"live"}`, live},
		{`{"type":"web_search","mode":null,"context_size":null,"allowed_domains":null,"location":null}`, live},
		{`{"type":"web_search","mode":"cached"}`, `{"type":"web_search","mode":"cached",` + defaults},
		{`{"type":"web_search","mode":"disabled"}`, `{"type":"web_search","mode":"disabled",` + defaults},
		{`{"type":"web_search","context_size":"low"}`, `{"type":"web_search","mode":"live","context_size":"low","allowed_domains":null,"location":null}`},
		{`{"type":"web_search","mode":"live","allowed_domains":[]}`, `{"type":"web_search","mode":"live","context_size":"medium","allowed_domains":[],"location":null}`},
		{`{"type":"web_search","mode":"cached","context_size":"high","allowed_domains":["example.com"],"location":{"country":"FR","city":"Paris"}}`,
			`{"type":"web_search","mode":"cached","context_size":"high","allowed_domains":["example.com"],"location":{"city":"Paris","country":"FR","region":null,"timezone":null}}`},
		{`{"type":"web_search","location":{}}`, `{"type":"web_search","mode":"live","context_size":"medium","allowed_domains":null,"location":{"city":null,"country":null,"region":null,"timezone":null}}`},
	} {
		saved, err := resolveSavedTools([]json.RawMessage{json.RawMessage(tc.input)})
		if err != nil || len(saved) != 1 || string(saved[0]) != tc.want {
			t.Fatalf("%s: %s %v", tc.input, saved, err)
		}
		// Saving is idempotent, so retrieved records resolve to the same bytes.
		again, err := resolveSavedTools(saved)
		if err != nil || string(again[0]) != tc.want {
			t.Fatalf("%s: resaved %s %v", tc.input, again, err)
		}
		executed, err := resolveSessionTools(saved)
		if err != nil || string(executed[0]) != tc.want {
			t.Fatalf("%s: Session resolution changed the saved tool: %s %v", tc.input, executed, err)
		}
	}
}

// TV-05 W1: Agent create echoes the saved projection.
func TestSavedWebSearchAgentCreate(t *testing.T) {
	h, s := configurationHandler(t, nil)
	w := credentialRequest(h, http.MethodPost, "/v1/agents", `{"model":"m","tools":[{"type":"web_search"},{"type":"programmatic_tool_calling","enabled":true}]}`)
	var agent struct{ Tools []json.RawMessage }
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &agent) != nil || len(agent.Tools) != 2 || s.writes != 1 ||
		string(agent.Tools[0]) != `{"type":"web_search","mode":"live","context_size":"medium","allowed_domains":null,"location":null}` {
		t.Fatal(w.Code, w.Body)
	}
	if w := credentialRequest(h, http.MethodPost, "/v1/agents/"+uuid.NewString(), `{"tools":[{"type":"web_search","mode":"cached"}]}`); w.Code != http.StatusOK || s.writes != 2 {
		t.Fatal(w.Code, w.Body)
	}
}

// TV-05 W4-W7: a Session keeps the saved search mode, an inline omitted mode
// resolves to live, and a per-Session tools replacement wins.
func TestSavedWebSearchSessionAdmission(t *testing.T) {
	saved := func(tool string) string {
		return `{"model":"m","multi_agent":{"enabled":false,"max_concurrent_subagents":null},"reasoning":{},"service_tier":"auto","text":{"format":{"type":"text"},"verbosity":"medium"},"tools":[` + tool + `]}`
	}
	const cached = `{"type":"web_search","mode":"cached","context_size":"medium","allowed_domains":[],"location":null}`
	id := uuid.NewString()
	h, _ := configurationHandler(t, map[string]string{id: saved(cached)})
	for _, tc := range []struct{ body, want string }{
		{`{"agent_id":"` + id + `","environment":{"type":"none"},"input":"x"}`, cached},
		{`{"agent_id":"` + id + `","agent":{"instructions":"x"},"environment":{"type":"none"},"input":"x"}`, cached},
		{`{"agent_id":"` + id + `","agent":{"tools":[{"type":"web_search"}]},"environment":{"type":"none"},"input":"x"}`, `{"type":"web_search","mode":"live","context_size":"medium","allowed_domains":null,"location":null}`},
		{`{"agent":{"model":"m","tools":[{"type":"web_search","mode":null}]},"environment":{"type":"none"},"input":"x"}`, `{"type":"web_search","mode":"live","context_size":"medium","allowed_domains":null,"location":null}`},
	} {
		w := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", tc.body)
		var session struct {
			Agent struct{ Tools []json.RawMessage }
		}
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &session) != nil || len(session.Agent.Tools) != 1 || string(session.Agent.Tools[0]) != tc.want {
			t.Fatal(tc.body, w.Code, w.Body)
		}
	}
	for _, tools := range []string{`[]`, `null`} {
		w := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", `{"agent_id":"`+id+`","agent":{"tools":`+tools+`},"environment":{"type":"none"},"input":"x"}`)
		var session struct {
			Agent struct{ Tools []json.RawMessage }
		}
		if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &session) != nil || len(session.Agent.Tools) != 0 {
			t.Fatal(tools, w.Code, w.Body)
		}
	}
}
