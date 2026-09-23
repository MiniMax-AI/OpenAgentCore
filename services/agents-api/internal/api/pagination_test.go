package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const (
	invalidDigit = "Failed to deserialize query string: limit: invalid digit found in string"
	tooLarge     = "Failed to deserialize query string: limit: number too large to fit in target type"
	betaRange    = "limit must be between 1 and 100"
)

func TestPageLimitPolicies(t *testing.T) {
	type accepted struct {
		query string
		limit int
	}
	type rejected struct {
		query, message string
		code, param    any
	}
	for _, test := range []struct {
		name     string
		path     string
		read     func(http.ResponseWriter, *http.Request, ...string) (pageOptions, bool)
		accepted []accepted
		rejected []rejected
	}{
		{
			// Agents, Sessions, Items and Templates clamp 0 and values above 100.
			"beta clamped", "/v1/agents", readClampedPage,
			[]accepted{{"", 20}, {"limit=0", 1}, {"limit=1", 1}, {"limit=%2B5", 5}, {"limit=100", 100}, {"limit=101", 100}, {"limit=1000", 100}, {"limit=9223372036854775807", 100}},
			[]rejected{
				{"limit=-1", invalidDigit, "invalid_request_error", nil}, {"limit=-0", invalidDigit, "invalid_request_error", nil},
				{"limit=abc", invalidDigit, "invalid_request_error", nil}, {"limit=1.5", invalidDigit, "invalid_request_error", nil},
				{"limit=", invalidDigit, "invalid_request_error", nil}, {"limit=null", invalidDigit, "invalid_request_error", nil},
				{"limit=-99999999999999999999", invalidDigit, "invalid_request_error", nil},
				{"limit=9223372036854775808", tooLarge, "invalid_request_error", nil},
			},
		},
		{
			// Turns, Subagent and Artifact lists keep rejecting values outside 1–100.
			"beta strict", "/v1/agents/sessions/session/turns", readPage,
			[]accepted{{"", 20}, {"limit=1", 1}, {"limit=100", 100}},
			[]rejected{
				{"limit=0", betaRange, "invalid_request_error", nil}, {"limit=101", betaRange, "invalid_request_error", nil},
				{"limit=1000", betaRange, "invalid_request_error", nil}, {"limit=-1", invalidDigit, "invalid_request_error", nil},
				{"limit=abc", invalidDigit, "invalid_request_error", nil}, {"limit=99999999999999999999", tooLarge, "invalid_request_error", nil},
			},
		},
		{
			"skills", "/v1/skills", readPage,
			[]accepted{{"", 20}, {"limit=0", 0}, {"limit=1", 1}, {"limit=100", 100}},
			[]rejected{
				{"limit=101", "Invalid 'limit': integer above maximum value. Expected a value <= 100, but got 101 instead.", "integer_above_max_value", "limit"},
				{"limit=-1", "Invalid 'limit': integer below minimum value. Expected a value >= 0, but got -1 instead.", "integer_below_min_value", "limit"},
				{"limit=abc", "limit must be an integer between 0 and 100.", "invalid_request", nil},
				{"limit=", "limit must be an integer between 0 and 100.", "invalid_request", nil},
			},
		},
		{
			"skill versions", "/v1/skills/skill-example/versions", readPage,
			[]accepted{{"limit=0", 0}, {"limit=100", 100}},
			[]rejected{
				{"limit=1000", "Invalid 'limit': integer above maximum value. Expected a value <= 100, but got 1000 instead.", "integer_above_max_value", "limit"},
				{"limit=-7", "Invalid 'limit': integer below minimum value. Expected a value >= 0, but got -7 instead.", "integer_below_min_value", "limit"},
			},
		},
	} {
		for _, want := range test.accepted {
			t.Run(test.name+"/"+want.query, func(t *testing.T) {
				w := httptest.NewRecorder()
				page, ok := test.read(w, httptest.NewRequest(http.MethodGet, test.path+"?"+want.query, nil))
				if !ok || page.limit != want.limit || w.Body.Len() != 0 {
					t.Fatalf("page=%+v ok=%t response=%s", page, ok, w.Body.String())
				}
			})
		}
		for _, want := range test.rejected {
			t.Run(test.name+"/"+want.query, func(t *testing.T) {
				w := httptest.NewRecorder()
				if _, ok := test.read(w, httptest.NewRequest(http.MethodGet, test.path+"?"+want.query, nil)); ok {
					t.Fatal("invalid limit accepted")
				}
				assertListQueryError(t, w, want.code, want.param, want.message)
			})
		}
	}
}

func TestListQueryIgnoresUnknownKeys(t *testing.T) {
	for _, path := range []string{"/v1/agents", "/v1/agents/sessions/session/turns", "/v1/files", "/v1/skills", "/v1/skills/skill-example/versions"} {
		for _, unknown := range []string{"unknown=1", "tenant_id=other", "limit[]=500", "Limit=0", "order[]=sideways", "after[]=x&after[]=y", "unknown=1&unknown=2"} {
			t.Run(path+"?"+unknown, func(t *testing.T) {
				read := func(query string) (pageOptions, bool, string) {
					w := httptest.NewRecorder()
					page, ok := readPage(w, httptest.NewRequest(http.MethodGet, path+"?"+query, nil))
					return page, ok, w.Body.String()
				}
				want, wantOK, _ := read("after=cursor&limit=2&order=asc")
				got, ok, body := read("after=cursor&limit=2&order=asc&" + unknown)
				if !wantOK || !ok || got != want || body != "" {
					t.Fatalf("unknown key changed the page: %+v %t %s", got, ok, body)
				}
			})
		}
	}
}

func TestListDuplicateKeyErrors(t *testing.T) {
	skills := "Duplicate parameter: '%[1]s'. You provided multiple values for this parameter, whereas only one is allowed. If you are trying to provide a list of values, use the array syntax instead e.g. '%[1]s[]=<value>'."
	for _, test := range []struct {
		path, key   string
		extra       []string
		code, param any
		message     string
	}{
		{"/v1/agents", "limit", nil, "invalid_request_error", nil, "Failed to deserialize query string: duplicate field `%s`"},
		{"/v1/agents", "order", nil, "invalid_request_error", nil, "Failed to deserialize query string: duplicate field `%s`"},
		{"/v1/agents/sessions", "after", []string{"agent_id"}, "invalid_request_error", nil, "Failed to deserialize query string: duplicate field `%s`"},
		{"/v1/agents/sessions", "agent_id", []string{"agent_id"}, "invalid_request_error", nil, "Failed to deserialize query string: duplicate field `%s`"},
		{"/v1/agents/sessions/session/artifacts", "environment_id", []string{"environment_id"}, "invalid_request_error", nil, "Failed to deserialize query string: duplicate field `%s`"},
		{"/v1/skills", "limit", nil, "duplicate_parameter", "limit", skills},
		{"/v1/skills/skill-example/versions", "order", nil, "duplicate_parameter", "order", skills},
		{"/v1/skills", "after", nil, "duplicate_parameter", "after", skills},
	} {
		// Identical and different repeated values are both duplicates.
		for _, values := range [][2]string{{"1", "1"}, {"1", "2"}} {
			t.Run(test.path+"/"+test.key+"="+values[1], func(t *testing.T) {
				w := httptest.NewRecorder()
				query := url.Values{test.key: values[:], "unknown": {"ignored"}}
				r := httptest.NewRequest(http.MethodGet, test.path+"?"+query.Encode(), nil)
				if _, ok := readPage(w, r, test.extra...); ok {
					t.Fatal("repeated key accepted")
				}
				assertListQueryError(t, w, test.code, test.param, fmt.Sprintf(test.message, test.key))
			})
		}
	}
}

func TestPageOrderPolicy(t *testing.T) {
	for _, path := range []string{"/v1/agents", "/v1/files", "/v1/skills", "/v1/skills/skill-example/versions"} {
		for _, test := range []struct {
			query     string
			ascending bool
		}{{"", false}, {"order=asc", true}, {"order=desc", false}} {
			t.Run(path+"?"+test.query, func(t *testing.T) {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodGet, path+"?"+test.query, nil)
				page, ok := readPage(w, r)
				if !ok || page.ascending != test.ascending || page.limit != 20 || w.Body.Len() != 0 {
					t.Fatalf("page=%+v ok=%t response=%s", page, ok, w.Body.String())
				}
			})
		}
	}
}

func TestListOrderErrorEnvelopes(t *testing.T) {
	for _, test := range []struct {
		path        string
		code, param any
		message     string
	}{
		{"/v1/agents", "invalid_request_error", nil, "Failed to deserialize query string: order: unknown variant `%s`, expected `asc` or `desc`"},
		{"/v1/agents/environments/environment-example/files", "invalid_request_error", nil, "Failed to deserialize query string: order: unknown variant `%s`, expected `asc` or `desc`"},
		{"/v1/files", nil, nil, "order must be asc or desc."},
		{"/v1/skills", "invalid_value", "order", "Invalid value: '%s'. Supported values are: 'asc' and 'desc'."},
		{"/v1/skills/skill-example/versions", "invalid_value", "order", "Invalid value: '%s'. Supported values are: 'asc' and 'desc'."},
	} {
		for _, order := range []string{"", "invalid", "ASC", " "} {
			t.Run(test.path+"/"+order, func(t *testing.T) {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodGet, test.path+"?order="+url.QueryEscape(order), nil)
				if _, ok := readPage(w, r); ok {
					t.Fatal("invalid order accepted")
				}
				message := test.message
				if test.code != nil {
					message = fmt.Sprintf(message, order)
				}
				assertListQueryError(t, w, test.code, test.param, message)
			})
		}
	}
}

// The Skills order error repeats the value only when it is short and printable,
// so a long or control-character value cannot inflate the response.
func TestSkillsOrderErrorBoundsEcho(t *testing.T) {
	const bounded = "Invalid value. Supported values are: 'asc' and 'desc'."
	for _, order := range []string{strings.Repeat("x", 257), strings.Repeat("\x01", 100), "bad\nvalue", "\xff"} {
		for _, path := range []string{"/v1/skills", "/v1/skills/skill-example/versions"} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, path+"?order="+url.QueryEscape(order), nil)
			if _, ok := readPage(w, r); ok {
				t.Fatal("invalid order accepted")
			}
			assertListQueryError(t, w, "invalid_value", "order", bounded)
		}
	}
	w := httptest.NewRecorder()
	if _, ok := readPage(w, httptest.NewRequest(http.MethodGet, "/v1/skills?order="+strings.Repeat("y", 256), nil)); ok {
		t.Fatal("invalid order accepted")
	}
	assertListQueryError(t, w, "invalid_value", "order", "Invalid value: '"+strings.Repeat("y", 256)+"'. Supported values are: 'asc' and 'desc'.")
}

func TestListQueryErrorPrecedence(t *testing.T) {
	skillsOrder := "Invalid value: ''. Supported values are: 'asc' and 'desc'."
	for _, test := range []struct {
		path, query string
		code, param any
		message     string
	}{
		// Duplicates precede value errors, and limit errors precede order errors.
		{"/v1/skills", "order=&limit=101", "integer_above_max_value", "limit", "Invalid 'limit': integer above maximum value. Expected a value <= 100, but got 101 instead."},
		{"/v1/skills", "order=&limit=0", "invalid_value", "order", skillsOrder},
		{"/v1/skills", "order=&tenant_id=other", "invalid_value", "order", skillsOrder},
		{"/v1/skills", "limit=-1&order=asc&order=desc", "duplicate_parameter", "order", "Duplicate parameter: 'order'. You provided multiple values for this parameter, whereas only one is allowed. If you are trying to provide a list of values, use the array syntax instead e.g. 'order[]=<value>'."},
		{"/v1/agents", "order=&limit=-1", "invalid_request_error", nil, invalidDigit},
		{"/v1/agents", "limit=abc&order=&order=", "invalid_request_error", nil, "Failed to deserialize query string: duplicate field `order`"},
	} {
		t.Run(test.path+"?"+test.query, func(t *testing.T) {
			w := httptest.NewRecorder()
			if _, ok := readPage(w, httptest.NewRequest(http.MethodGet, test.path+"?"+test.query, nil)); ok {
				t.Fatal("invalid query accepted")
			}
			assertListQueryError(t, w, test.code, test.param, test.message)
		})
	}
}

func assertListQueryError(t *testing.T, w *httptest.ResponseRecorder, code, param any, message string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"error": map[string]any{
		"type": "invalid_request_error", "code": code, "param": param, "message": message,
	}}
	if w.Code != http.StatusBadRequest || !reflect.DeepEqual(body, want) {
		t.Fatalf("error envelope: status=%d body=%s want=%v", w.Code, w.Body.String(), want)
	}
	if w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("error headers changed: %v", w.Header())
	}
}
