package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

type nodeArtifact struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

type nodeManifest struct {
	SourceCommit    string                  `json:"source_commit"`
	ArtifactBaseURL string                  `json:"artifact_base_url"`
	Artifacts       map[string]nodeArtifact `json:"artifacts"`
}

func (h *console) readNodeManifest(prefix string) (nodeManifest, error) {
	var manifest nodeManifest
	f, err := h.nodePayload.Open(prefix + "manifest.json")
	if err != nil {
		return manifest, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 || json.Unmarshal(raw, &manifest) != nil {
		return manifest, errors.New("invalid node manifest")
	}
	if prefix != "" && prefix != "releases/"+manifest.SourceCommit+"/" {
		return manifest, errors.New("node manifest release mismatch")
	}
	return manifest, nil
}

var nodeArtifactName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
var nodeArtifactDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Release locations come only from the installed, verified distribution manifest.
// Web redirects missing artifacts instead of downloading or caching them itself.
func (m nodeManifest) artifactURL(filename string) string {
	if !payloadRevision.MatchString(m.SourceCommit) || !nodeArtifactName.MatchString(filename) || !strings.Contains(filename, m.SourceCommit) {
		return ""
	}
	allowed := false
	for logical, entry := range m.Artifacts {
		if optionalPayloadFiles[logical] && entry.Filename == filename && entry.Size > 0 && nodeArtifactDigest.MatchString(entry.SHA256) {
			allowed = true
			break
		}
	}
	if !allowed {
		return ""
	}
	base, err := url.Parse(m.ArtifactBaseURL)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || strings.Trim(base.Path, "/") == "" || strings.Contains(m.ArtifactBaseURL, "\\") || strings.IndexFunc(m.ArtifactBaseURL, unicode.IsSpace) >= 0 {
		return ""
	}
	for _, part := range strings.Split(strings.ToLower(base.Path), "/") {
		if part == "latest" {
			return ""
		}
	}
	return strings.TrimRight(base.String(), "/") + "/" + filename
}
