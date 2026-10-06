package main

import (
	"net/http"
	"strings"
)

// coreDirectRequest reports the application (/v1), machine connection
// (/api/v1) and API reference (/docs) namespaces. The console forwards them
// to Core unchanged and adds no credential of its own.
func coreDirectRequest(r *http.Request) bool {
	for _, prefix := range []string{"/v1", "/api/v1", "/docs"} {
		if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
			return true
		}
	}
	return false
}

// coreRequest reports a /core/v1 operation. After console sign-in and the
// same-origin checks, the console forwards each one with the Core key; Core
// alone decides whether the route exists. ServeHTTP has already rejected
// literal or encoded dot segments, empty segments, backslashes, double
// encoding and upgrades, so the request cannot leave /core/v1 on Core.
func coreRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/core/v1/")
}
