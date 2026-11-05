package api

import (
	"net/http"
	"testing"
)

// Official fields without a native home are rejected by param, before storage.
func TestUnsupportedOfficialFieldsAreRejectedByParam(t *testing.T) {
	h, s := validationHandler(t)
	for _, test := range []struct{ path, body, param string }{
		{"/v1/agents/sessions/s", `{"agent":{"tools":[]}}`, "agent.tools"},
		{"/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"openai_hosted","container_size":"small"},"input":"hi"}`, "container_size"},
		{"/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"openai_hosted","desktop":{"enabled":true}},"input":"hi"}`, "desktop.enabled"},
		{"/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"openai_hosted","network":{"access":"enabled","blocked_domains":["a.example"]}},"input":"hi"}`, "network.blocked_domains"},
		{"/v1/agents/environments/templates", `{"desktop":{"enabled":true}}`, "desktop.enabled"},
		{"/v1/agents/environments/templates", `{"desktop":7}`, "desktop"},
		{"/v1/agents/environments/templates", `{"desktop":{"enabled":"yes"}}`, "desktop.enabled"},
		{"/v1/agents/environments/templates", `{"desktop":{}}`, "desktop.enabled"},
		{"/v1/agents/environments/templates", `{"desktop":{"enabled":false,"x":1}}`, "desktop.x"},
		{"/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"openai_hosted","environment_id":null},"input":"hi"}`, "environment_id"},
		{"/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"openai_hosted","container_size":null},"input":"hi"}`, "container_size"},
		{"/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"openai_hosted","desktop":{"enabled":"yes"}},"input":"hi"}`, "desktop.enabled"},
		{"/v1/agents/sessions/s/events", `{"events":[{"type":"agent.session.input.computer_use_approval_request_result","request_id":"r","response":{"type":"browser_origin_access","decision":"approve"}}]}`, "events[0].type"},
	} {
		w := bodyGateRequest(h, test.path, "application/json", []byte(test.body))
		_, param, _ := errorFields(t, w)
		if w.Code != http.StatusBadRequest || param == nil || *param != test.param {
			t.Errorf("%s %s: %d %s", test.path, test.body, w.Code, w.Body)
		}
	}
	if s.writes != 0 {
		t.Fatalf("a rejected request reached storage: %d writes", s.writes)
	}
}
