package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Installation control is a narrow authenticated Unix-socket request. Neither
// the console nor Core receives a Docker socket or arbitrary installer commands.
func (h *console) serveInstallationDomain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.installationSocket == "" {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			consoleCoreError(w, http.StatusBadRequest, "domain_setup_unavailable", "This installation uses an external reverse proxy")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"supported": false, "state": "unconfigured", "public_url": nil, "target_url": nil,
			"message": "This installation uses an external reverse proxy. Configure HTTPS there, then set public_url and run oac apply."})
		return
	}
	body := http.MaxBytesReader(w, r.Body, 2048)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		consoleCoreError(w, http.StatusBadRequest, "invalid_request", "Request body is too large")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, r.Method, "http://localhost/domain", strings.NewReader(string(data)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.coreKey)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", h.installationSocket)
	}}
	defer transport.CloseIdleConnections()
	response, err := transport.RoundTrip(req)
	if err != nil {
		consoleCoreError(w, http.StatusBadGateway, "installation_unreachable", "Installation management is unavailable; run oac status on the server")
		return
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(payload) > 16384 || !json.Valid(payload) || response.StatusCode < 200 || response.StatusCode >= 500 {
		consoleCoreError(w, http.StatusBadGateway, "installation_unreachable", "Installation management is unavailable; run oac status on the server")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(payload)
}

// Only literal IP addresses are accepted during first-run HTTP setup. Hostnames
// still require the configured origin, so DNS rebinding cannot open the console.
func (h *console) requestOrigin(r *http.Request) string {
	if h.bootstrap {
		host, port, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
			port = ""
		}
		if net.ParseIP(strings.Trim(host, "[]")) != nil && (port == "" || validPort(port)) {
			return "http://" + r.Host
		}
	}
	if r.Host == h.host {
		return h.origin
	}
	return ""
}

func validPort(value string) bool {
	port, err := strconv.Atoi(value)
	return err == nil && port > 0 && port <= 65535
}
