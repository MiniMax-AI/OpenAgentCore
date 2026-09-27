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
	"native/bin/parsar-sandbox-node": true, "native/bin/oac-daemon": true,
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

// installerDigest returns the SHA-256 that Web's install commands verify
// before running the named installer from the payload.
func installerDigest(root *os.Root, name string) (string, error) {
	f, err := root.Open(name)
	if err != nil {
		return "", errors.New("installer " + name + " is missing from the node installation payload")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return "", errors.New("invalid installer " + name)
	}
	digest := sha256.New()
	if _, err = io.Copy(digest, f); err != nil {
		return "", errors.New("cannot read installer " + name)
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

// providerArtifacts lists the artifacts a node of each provider downloads from
// this console, besides the fixed payload files.
var providerArtifacts = map[string][]string{
	"docker": {"native/bin/parsar-sandbox-node", "images/runtime.tar.gz"},
	"microsandbox": {"native/bin/parsar-sandbox-node", "images/runtime.tar.gz", "native/bin/agents-api-microsandbox-provider",
		"native/microsandbox/msb", "native/microsandbox/libkrunfw.so.5.6.1"},
}

// nodeArtifacts reports the providers whose node artifacts this console can serve:
// each is declared in manifest.json and present at its declared size. A thin
// distribution has none. Nodes verify every checksum themselves. It is read per
// request, so artifacts added by rerunning the installer show without a restart.
func (h *console) nodeArtifacts() []string {
	available := []string{}
	if h.nodePayload == nil {
		return available
	}
	f, err := h.nodePayload.Open("manifest.json")
	if err != nil {
		return available
	}
	defer f.Close()
	var manifest struct {
		Artifacts map[string]struct {
			Filename string `json:"filename"`
			Size     int64  `json:"size"`
		} `json:"artifacts"`
	}
	if json.NewDecoder(io.LimitReader(f, 1024*1024)).Decode(&manifest) != nil {
		return available
	}
	for _, provider := range []string{"docker", "microsandbox"} {
		complete := true
		for _, logical := range providerArtifacts[provider] {
			entry, ok := manifest.Artifacts[logical]
			info, err := h.nodePayload.Stat("artifacts/" + entry.Filename)
			if !ok || entry.Filename == "" || strings.Contains(entry.Filename, "/") || err != nil || !info.Mode().IsRegular() || info.Size() != entry.Size {
				complete = false
				break
			}
		}
		if complete {
			available = append(available, provider)
		}
	}
	return available
}

func (h *console) serveConsoleConfiguration(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		NodeInstaller             bool     `json:"node_installer"`
		NodeInstallerSHA256       string   `json:"node_installer_sha256"`
		NodeArtifacts             []string `json:"node_artifacts"`
		SelfHostedInstaller       bool     `json:"self_hosted_installer"`
		SelfHostedInstallerSHA256 string   `json:"self_hosted_installer_sha256"`
	}{h.nodePayload != nil, h.nodeInstallerDigest, h.nodeArtifacts(), h.nodePayload != nil, h.selfHostedInstallerDigest})
}
