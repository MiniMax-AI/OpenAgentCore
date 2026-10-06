package main

import "net/http"

// requestOrigin accepts only the configured origin, so a rebound DNS name
// cannot open the console.
func (h *console) requestOrigin(r *http.Request) string {
	if r.Host == h.host {
		return h.origin
	}
	return ""
}
