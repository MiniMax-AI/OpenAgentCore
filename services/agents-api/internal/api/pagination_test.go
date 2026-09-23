package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestPageSizePolicy(t *testing.T) {
	for _, test := range []struct {
		query              string
		cappedOK, strictOK bool
		limit              int
	}{
		{"", true, true, 20}, {"limit=1", true, true, 1}, {"limit=100", true, true, 100},
		{"limit=101", true, false, 100}, {"limit=9223372036854775807", true, false, 100},
		{"limit=9223372036854775808", false, false, 0}, {"limit=0", false, false, 0},
		{"limit=-1", false, false, 0}, {"limit=1.5", false, false, 0}, {"limit=", false, false, 0},
		{"limit=null", false, false, 0}, {"limit=2&limit=3", false, false, 0}, {"order=invalid", false, false, 0},
		{"order=", false, false, 0}, {"tenant_id=other", false, false, 0},
	} {
		t.Run(test.query, func(t *testing.T) {
			for _, strict := range []bool{true, false} {
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", "/v1/agents?"+test.query, nil)
				page, ok := readPageSize(w, r, strict)
				want := test.cappedOK
				if strict {
					want = test.strictOK
				}
				if ok != want || (ok && page.limit != test.limit) || (!ok && w.Code != 400) {
					t.Fatalf("strict=%t page=%+v ok=%t status=%d", strict, page, ok, w.Code)
				}
			}
		})
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

func TestListOrderValidationPreservesOtherQueryErrors(t *testing.T) {
	for _, test := range []struct{ query, code string }{
		{"order=&limit=0", "invalid_request"},
		{"order=asc&order=desc", "unsupported_parameter"},
		{"order=&tenant_id=other", "unsupported_parameter"},
	} {
		t.Run(test.query, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/v1/skills?"+test.query, nil)
			if _, ok := readPage(w, r); ok {
				t.Fatal("invalid query accepted")
			}
			var response struct {
				Error struct {
					Code  string
					Param *string
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusBadRequest || response.Error.Code != test.code || response.Error.Param != nil {
				t.Fatalf("other query error changed: %d %s", w.Code, w.Body.String())
			}
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
