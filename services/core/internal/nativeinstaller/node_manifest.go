package nativeinstaller

import (
	"encoding/json"
	"errors"
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
	allowed         map[string]bool
	object          map[string]any
}

func (p *nodePayload) readNodeManifest(prefix string) (nodeManifest, error) {
	raw, err := p.readMetadata(prefix + "manifest.json")
	if err != nil {
		return nodeManifest{}, err
	}
	return p.parseNodeManifest(prefix, raw)
}

func (p *nodePayload) parseNodeManifest(prefix string, raw []byte) (nodeManifest, error) {
	var manifest nodeManifest
	if json.Unmarshal(raw, &manifest.object) != nil {
		return manifest, errors.New("invalid node manifest")
	}
	manifest.SourceCommit, _ = manifest.object["source_commit"].(string)
	if value := manifest.object["artifact_base_url"]; value != nil {
		var ok bool
		manifest.ArtifactBaseURL, ok = value.(string)
		if !ok {
			return manifest, errors.New("invalid node artifact base URL")
		}
	}
	manifest.Artifacts = map[string]nodeArtifact{}
	entries, ok := manifest.object["artifacts"].(map[string]any)
	if manifest.object["artifacts"] != nil && !ok {
		return manifest, errors.New("invalid node artifacts")
	}
	for logical, value := range entries {
		object, ok := value.(map[string]any)
		if !ok {
			return manifest, errors.New("invalid node artifact")
		}
		var entry nodeArtifact
		entry.Filename, _ = object["filename"].(string)
		entry.SHA256, _ = object["sha256"].(string)
		if !nodeArtifactName.MatchString(entry.Filename) || !checksum.MatchString(entry.SHA256) {
			return manifest, errors.New("invalid node artifact identity")
		}
		size, _ := object["size"].(float64)
		if size <= 0 || size > 1<<53 || size != float64(int64(size)) {
			return manifest, errors.New("invalid node artifact size")
		}
		entry.Size = int64(size)
		manifest.Artifacts[logical] = entry
	}

	if prefix != "releases/"+manifest.SourceCommit+"/" {
		return manifest, errors.New("node manifest release mismatch")
	}
	manifest.allowed = p.allowed
	if manifest.ArtifactBaseURL != "" {
		for logical, entry := range manifest.Artifacts {
			if p.allowed[logical] && manifest.artifactURL(entry.Filename) == "" {
				return manifest, errors.New("invalid pinned node artifact URL")
			}
		}
	}
	return manifest, nil
}

var nodeArtifactName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Release locations come only from the installed, verified distribution manifest.
// Core redirects missing artifacts instead of downloading or caching them itself.
func (m nodeManifest) artifactURL(filename string) string {
	if !payloadRevision.MatchString(m.SourceCommit) || !nodeArtifactName.MatchString(filename) || !strings.Contains(filename, m.SourceCommit) {
		return ""
	}
	allowed := false
	for logical, entry := range m.Artifacts {
		if m.allowed[logical] && entry.Filename == filename && entry.Size > 0 && checksum.MatchString(entry.SHA256) {
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
