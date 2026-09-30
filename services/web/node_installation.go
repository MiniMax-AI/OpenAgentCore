package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/providerassets"
)

// These are distribution artifacts, never installation configuration or secrets.
// Serving the fixed list avoids a package registry or an arbitrary file endpoint.
var nodePayloadFiles = map[string]bool{
	"node-install.pyz": true,
	"manifest.json":    true, "SHA256SUMS": true, "runtime/seccomp.json": true,
}

// The generated adapter declarations bound the distribution payload endpoint.
var optionalPayloadFiles = func() map[string]bool {
	files := map[string]bool{}
	for _, artifacts := range providerassets.Catalog() {
		for _, artifact := range artifacts {
			files[artifact.Path] = true
		}
	}
	return files
}()

var payloadRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)

// activePayloadPrefix reads one atomic pointer per request. Legacy flat payloads
// remain readable until the installer publishes its first versioned release.
func activePayloadPrefix(root *os.Root) (string, error) {
	raw, err := root.ReadFile("active.json")
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || len(raw) > 256 {
		return "", errors.New("invalid active node payload")
	}
	var pointer struct {
		SourceCommit string `json:"source_commit"`
	}
	if json.Unmarshal(raw, &pointer) != nil || !payloadRevision.MatchString(pointer.SourceCommit) {
		return "", errors.New("invalid active node payload")
	}
	return "releases/" + pointer.SourceCommit + "/", nil
}

func (h *console) resolveNodePayload(name string) (string, bool) {
	prefix := ""
	if strings.HasPrefix(name, "releases/") {
		parts := strings.SplitN(name, "/", 3)
		if len(parts) != 3 || !payloadRevision.MatchString(parts[1]) {
			return "", false
		}
		prefix, name = "releases/"+parts[1]+"/", parts[2]
	} else {
		var err error
		prefix, err = activePayloadPrefix(h.nodePayload)
		if err != nil {
			return "", false
		}
	}
	if prefix == "" && nodePayloadFiles[name] {
		return name, true
	}
	if !nodePayloadFiles[name] && (!strings.HasPrefix(name, "artifacts/") || strings.Contains(strings.TrimPrefix(name, "artifacts/"), "/")) {
		return "", false
	}
	manifest, err := h.readNodeManifest(prefix)
	if err != nil {
		return "", false
	}
	if nodePayloadFiles[name] {
		return prefix + name, true
	}
	for logical, entry := range manifest.Artifacts {
		if optionalPayloadFiles[logical] && entry.Filename != "" && "artifacts/"+entry.Filename == name {
			return prefix + name, true
		}
	}
	return "", false
}

// installerDigest returns the SHA-256 that Web's install commands verify
// before running the named installer from the payload.
func installerDigest(root *os.Root, name string) (string, error) {
	prefix, err := activePayloadPrefix(root)
	if err != nil {
		return "", err
	}
	f, err := root.Open(prefix + name)
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
	resolved, allowed := h.resolveNodePayload(name)
	if !allowed {
		http.NotFound(w, r)
		return
	}
	f, err := h.nodePayload.Open(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if prefix, filename, ok := strings.Cut(resolved, "artifacts/"); ok {
				if manifest, readErr := h.readNodeManifest(prefix); readErr == nil {
					if target := manifest.artifactURL(filename); target != "" {
						http.Redirect(w, r, target, http.StatusTemporaryRedirect)
						return
					}
				}
			}
		}
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

// nodeArtifacts reports the providers whose node artifacts this console can serve:
// each is present locally or has a pinned release download. Nodes verify every
// checksum themselves. It is read per
// request, so artifacts added by rerunning the installer show without a restart.
func (h *console) nodeArtifacts() []string {
	available := []string{}
	if h.nodePayload == nil {
		return available
	}
	prefix, err := activePayloadPrefix(h.nodePayload)
	if err != nil {
		return available
	}
	manifest, err := h.readNodeManifest(prefix)
	if err != nil {
		return available
	}
	for provider, artifacts := range providerassets.Catalog() {
		complete := true
		for _, artifact := range artifacts {
			logical := artifact.Path
			entry, ok := manifest.Artifacts[logical]
			info, err := h.nodePayload.Stat(prefix + "artifacts/" + entry.Filename)
			local := err == nil && info.Mode().IsRegular() && info.Size() == entry.Size
			remote := errors.Is(err, os.ErrNotExist) && manifest.artifactURL(entry.Filename) != ""
			if !ok || entry.Filename == "" || strings.Contains(entry.Filename, "/") || (!local && !remote) {
				complete = false
				break
			}
		}
		if complete {
			available = append(available, provider)
		}
	}
	sort.Strings(available)
	return available
}

func (h *console) serveConsoleConfiguration(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		NodeInstaller       bool     `json:"node_installer"`
		NodeInstallerSHA256 string   `json:"node_installer_sha256"`
		NodeArtifacts       []string `json:"node_artifacts"`
	}{h.nodePayload != nil, h.nodeInstallerDigest, h.nodeArtifacts()})
}
