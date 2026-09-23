package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

const onlyDisabledSearch = "Only disabled web_search is qualified for execution."

// TV-05 W1/W2: saved web_search keeps every pinned mode with the official
// projection, while Session execution still qualifies only the disabled form.
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
		var mode struct{ Mode string }
		_ = json.Unmarshal(saved[0], &mode)
		if mode.Mode == "disabled" {
			if err != nil || string(executed[0]) != tc.want {
				t.Fatalf("%s: disabled execution changed: %s %v", tc.input, executed, err)
			}
		} else if err == nil || err.Error() != onlyDisabledSearch {
			t.Fatalf("%s: enabled search admitted: %v", tc.input, err)
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

// TV-05 W4-W7: a saved Agent with enabled search cannot reach execution through
// any creation mode; a Session tools replacement or disabled search is admitted.
func TestSavedWebSearchSessionAdmission(t *testing.T) {
	saved := func(tool string) string {
		return `{"model":"m","multi_agent":{"enabled":false,"max_concurrent_subagents":null},"reasoning":{},"service_tier":"auto","text":{"format":{"type":"text"},"verbosity":"medium"},"tools":[` + tool + `]}`
	}
	enabled := map[string]string{}
	for _, mode := range []string{"live", "cached"} {
		enabled[uuid.NewString()] = saved(`{"type":"web_search","mode":"` + mode + `","context_size":"medium","allowed_domains":null,"location":null}`)
	}
	disabled := uuid.NewString()
	agents := map[string]string{disabled: saved(`{"type":"web_search","mode":"disabled","context_size":"medium","allowed_domains":[],"location":null}`)}
	for id, configuration := range enabled {
		agents[id] = configuration
	}
	h, s := configurationHandler(t, agents)
	for id := range enabled {
		for _, suffix := range []string{
			`"environment":{"type":"none"},"input":"x"}`,
			`"environment":{"type":"none"},"input":"x","stream":true}`,
			`"environment":{"type":"self_hosted","workspace_directory":"/workspace"},"input":"x"}`,
			`"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`,
			`"environment":{"type":"openai_hosted"}}`,
			// Replacing another field keeps the saved tools.
			`"agent":{"instructions":"x"},"environment":{"type":"none"},"input":"x"}`,
		} {
			w := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", `{"agent_id":"`+id+`",`+suffix)
			assertConfigurationError(t, w, "unsupported_or_invalid_configuration", nil, onlyDisabledSearch)
		}
		// C4: the input requirement still precedes execution admission.
		w := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", `{"agent_id":"`+id+`","environment":{"type":"none"}}`)
		assertConfigurationError(t, w, "invalid_request_error", nil, "conversation-only sessions currently require initial input")
	}
	// W6: inline enabled or omitted-mode search, also as a saved-Agent override.
	for _, tool := range []string{`{"type":"web_search"}`, `{"type":"web_search","mode":null}`, `{"type":"web_search","mode":"live"}`, `{"type":"web_search","mode":"cached"}`} {
		for _, body := range []string{
			`{"agent":{"model":"m","tools":[` + tool + `]},"environment":{"type":"none"},"input":"x"}`,
			`{"agent_id":"` + disabled + `","agent":{"tools":[` + tool + `]},"environment":{"type":"none"},"input":"x"}`,
		} {
			assertConfigurationError(t, credentialRequest(h, http.MethodPost, "/v1/agents/sessions", body), "unsupported_or_invalid_configuration", nil, onlyDisabledSearch)
		}
	}
	if s.writes != 0 {
		t.Fatal("rejected configuration reached storage")
	}
	// W5 and W7: a per-Session tools replacement and saved disabled search are admitted.
	for id := range enabled {
		for _, tools := range []string{`[]`, `null`, `[{"type":"web_search","mode":"disabled"}]`} {
			w := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", `{"agent_id":"`+id+`","agent":{"tools":`+tools+`},"environment":{"type":"none"},"input":"x"}`)
			var session struct {
				Agent struct{ Tools []json.RawMessage }
			}
			if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &session) != nil {
				t.Fatal(tools, w.Code, w.Body)
			}
			for _, tool := range session.Agent.Tools {
				if string(tool) != `{"type":"web_search","mode":"disabled","context_size":"medium","allowed_domains":null,"location":null}` {
					t.Fatalf("saved search leaked into the Session: %s", w.Body)
				}
			}
		}
	}
	w := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", `{"agent_id":"`+disabled+`","environment":{"type":"none"},"input":"x"}`)
	var session struct {
		Agent struct{ Tools []json.RawMessage }
	}
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &session) != nil || len(session.Agent.Tools) != 1 ||
		string(session.Agent.Tools[0]) != `{"type":"web_search","mode":"disabled","context_size":"medium","allowed_domains":[],"location":null}` {
		t.Fatal(w.Code, w.Body)
	}
}
