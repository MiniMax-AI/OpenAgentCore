package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
)

// These are distribution artifacts, never installation configuration or secrets.
// Serving the fixed list avoids a package registry or an arbitrary file endpoint.
var nodePayloadFiles = map[string]bool{
	"node-install.pyz": true, "self-hosted-install.pyz": true,
	"manifest.json": true, "SHA256SUMS": true, "runtime/seccomp.json": true,
}

// An offline distribution exposes only artifacts declared for these payloads.
var optionalPayloadFiles = map[string]bool{
	"native/bin/parsar-sandbox-node": true, "native/bin/parsar-daemon": true,
	"native/bin/parsar-runtime": true, "native/bin/agents-api-microsandbox-provider": true,
	"native/microsandbox/msb": true, "native/microsandbox/libkrunfw.so.5.6.1": true,
	"images/runtime.tar.gz": true, "runtime/seccomp.json": true,
}

func (h *console) allowedNodePayload(name string) bool {
	if nodePayloadFiles[name] {
		return true
	}
	if !strings.HasPrefix(name, "artifacts/") || strings.Contains(strings.TrimPrefix(name, "artifacts/"), "/") {
		return false
	}
	f, err := h.nodePayload.Open("manifest.json")
	if err != nil {
		return false
	}
	defer f.Close()
	var manifest struct {
		Artifacts map[string]struct {
			Filename string `json:"filename"`
		} `json:"artifacts"`
	}
	if json.NewDecoder(io.LimitReader(f, 1024*1024)).Decode(&manifest) != nil {
		return false
	}
	for logical, entry := range manifest.Artifacts {
		if optionalPayloadFiles[logical] && entry.Filename != "" && "artifacts/"+entry.Filename == name {
			return true
		}
	}
	return false
}

func nodeInstallerDigest(root *os.Root) (string, error) {
	f, err := root.Open("node-install.pyz")
	if err != nil {
		return "", errors.New("node installer is missing")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return "", errors.New("invalid node installer")
	}
	digest := sha256.New()
	if _, err = io.Copy(digest, f); err != nil {
		return "", errors.New("cannot read node installer")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (h *console) serveNodePayload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/node-install/")
	if !h.allowedNodePayload(name) {
		http.NotFound(w, r)
		return
	}
	f, err := h.nodePayload.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func (h *console) serveConsoleConfiguration(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		SandboxAdmin        bool   `json:"sandbox_admin"`
		NodeInstaller       bool   `json:"node_installer"`
		NodeInstallerSHA256 string `json:"node_installer_sha256"`
	}{h.adminToken != "", h.nodePayload != nil, h.nodeInstallerDigest})
}

// Node and Runtime credentials pass through unchanged to Core authentication.
// Console or project credentials are never substituted on these public routes.
func nodeTransportRequest(r *http.Request) bool {
	switch r.URL.Path {
	case "/core/v1/sandbox/enroll", "/api/v1/agent-daemon/enroll", "/api/v1/agent-daemon/bootstrap":
		return r.Method == http.MethodPost && r.Header.Get("Upgrade") == ""
	case "/core/v1/sandbox/node/identity", "/api/v1/agent-daemon/device-status", "/api/v1/agent-daemon/connection":
		return r.Method == http.MethodGet && r.Header.Get("Upgrade") == ""
	case "/core/v1/sandbox/node/connect", "/api/v1/agent-daemon/ws":
		return r.Method == http.MethodGet && strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	}
	return false
}

// Environment credentials use project ownership, never deployment administration.
func projectExtensionRequest(r *http.Request) bool {
	const prefix = "/core/v1/environments/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] != "executor-credentials" {
		return false
	}
	return len(parts) == 2 && r.Method == http.MethodPost || len(parts) == 3 && parts[2] != "" && r.Method == http.MethodDelete
}
