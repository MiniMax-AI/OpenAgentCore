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
	"node_install.py": true, "manifest.json": true, "SHA256SUMS": true,
	"native/bin/parsar-sandbox-node":              true,
	"native/bin/agents-api-microsandbox-provider": true,
	"native/microsandbox/msb":                     true,
	"native/microsandbox/libkrunfw.so.5.6.1":      true,
	"images/runtime.tar":                          true, "runtime/seccomp.json": true,
}

func nodeInstallerDigest(root *os.Root) (string, error) {
	f, err := root.Open("node_install.py")
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
	if !nodePayloadFiles[name] {
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
	case "/core/v1/sandbox/enroll", "/api/v1/agent-daemon/bootstrap":
		return r.Method == http.MethodPost && r.Header.Get("Upgrade") == ""
	case "/core/v1/sandbox/node/identity", "/api/v1/agent-daemon/device-status":
		return r.Method == http.MethodGet && r.Header.Get("Upgrade") == ""
	case "/core/v1/sandbox/node/connect", "/api/v1/agent-daemon/ws":
		return r.Method == http.MethodGet && strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	}
	return false
}
