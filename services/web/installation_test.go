package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOnlyTheConfiguredOriginOpensTheConsole(t *testing.T) {
	h := &console{config: config{origin: "http://127.0.0.1:8080"}, host: "127.0.0.1:8080"}
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"203.0.113.10:8080", false},
		{"[2001:db8::1]:8080", false},
		{"evil.example:8080", false},
	} {
		t.Run(tc.host, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/", nil)
			r.Host = tc.host
			r.Header.Set("Origin", "http://"+tc.host)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			if got := h.sameOrigin(r); got != tc.want {
				t.Fatalf("sameOrigin=%v, want %v", got, tc.want)
			}
		})
	}
}
