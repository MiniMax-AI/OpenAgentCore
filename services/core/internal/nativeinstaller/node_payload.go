package nativeinstaller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"regexp"
	"strings"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/providerassets"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

const nodeInstallPath = "/api/v1/sandbox-node/install/"

var payloadRevision = regexp.MustCompile("^(?:" + sandbox.SourceCommitPattern + ")$")
var nodePayloadFiles = map[string]bool{
	"node-install.pyz": true, "manifest.json": true, "SHA256SUMS": true, "runtime/seccomp.json": true,
}

// NodeInstallation reports the matched installer and currently servable releases.
// Its source revision is the installation's source_commit.
type NodeInstallation struct {
	InstallerSHA256 string                            `json:"installer_sha256" binding:"required"`
	RuntimeReleases map[string]sandbox.RuntimeRelease `json:"runtime_releases" binding:"required"`
}

type nodePayload struct {
	root      *os.Root
	prefix    string
	digest    string
	manifest  nodeManifest
	releases  map[string]sandbox.RuntimeRelease
	artifacts map[string][]providerassets.Artifact
	allowed   map[string]bool
}

func loadNodes(directory, version string, registry *providers.Registry) (_ *nodePayload, err error) {
	root, err := os.OpenRoot(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			root.Close()
		}
	}()
	raw, err := root.ReadFile("active.json")
	var active struct {
		SourceCommit string `json:"source_commit"`
	}
	if err != nil || len(raw) > 256 || json.Unmarshal(raw, &active) != nil || !payloadRevision.MatchString(version) || active.SourceCommit != version {
		return nil, errors.New("node payload does not match this Core build")
	}
	artifacts, err := registry.ArtifactCatalog()
	if err != nil {
		return nil, err
	}
	p := &nodePayload{root: root, prefix: "releases/" + version + "/", artifacts: artifacts, allowed: map[string]bool{}, releases: map[string]sandbox.RuntimeRelease{}}
	for _, entries := range artifacts {
		for _, artifact := range entries {
			p.allowed[artifact.Path] = true
		}
	}
	// Initialization verifies immutable metadata before publishing it. Verify it
	// here as well for installations assembled without the initialization image.
	sumsRaw, err := p.readMetadata(p.prefix + "SHA256SUMS")
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(sumsRaw)), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || !checksum.MatchString(sum) || sums[name] != "" {
			return nil, errors.New("invalid node metadata checksums")
		}
		sums[name] = sum
	}
	var manifestRaw []byte
	for name := range nodePayloadFiles {
		if name == "SHA256SUMS" {
			continue
		}
		data, e := p.readMetadata(p.prefix + name)
		if e != nil {
			return nil, e
		}
		if name == "manifest.json" {
			manifestRaw = data
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != sums[name] {
			return nil, fmt.Errorf("node metadata checksum mismatch: %s", name)
		}
	}
	p.digest = sums["node-install.pyz"]
	p.manifest, err = p.parseNodeManifest(p.prefix, manifestRaw)
	if err != nil {
		return nil, err
	}
	if p.manifest.object["platform"] != "linux/amd64" {
		return nil, errors.New("node payload requires Linux amd64")
	}
	for provider := range artifacts {
		adapter, e := registry.Lookup(provider)
		if e != nil {
			return nil, e
		}
		release := sandbox.RuntimeRelease{SourceCommit: version, Artifacts: map[string]string{}}
		for name, rule := range adapter.Policy.Artifacts {
			var value any = p.manifest.object
			for _, key := range rule.ManifestPath {
				object, ok := value.(map[string]any)
				if !ok {
					value = nil
					break
				}
				value = object[key]
			}
			release.Artifacts[name], _ = value.(string)
		}
		if e = release.Validate(adapter.Policy.Artifacts); e != nil {
			return nil, fmt.Errorf("invalid node distribution for %s: %w", provider, e)
		}
		p.releases[provider] = release
	}
	p.manifest.object = nil
	return p, nil
}

func (p *nodePayload) readMetadata(name string) ([]byte, error) {
	f, err := p.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("invalid node metadata file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if len(raw) > 1<<20 {
		return nil, errors.New("node metadata is too large")
	}
	return raw, err
}

func (p *nodePayload) available(provider string) bool {
	entries, ok := p.artifacts[provider]
	if !ok {
		return false
	}
	for _, artifact := range entries {
		entry, ok := p.manifest.Artifacts[artifact.Path]
		if !ok || !nodeArtifactName.MatchString(entry.Filename) || entry.Size <= 0 || !checksum.MatchString(entry.SHA256) {
			return false
		}
		info, err := p.root.Stat(p.prefix + "artifacts/" + entry.Filename)
		local := err == nil && info.Mode().IsRegular() && info.Size() == entry.Size
		remote := errors.Is(err, os.ErrNotExist) && p.manifest.artifactURL(entry.Filename) != ""
		if !local && !remote {
			return false
		}
	}
	return true
}

// RuntimeRelease rechecks local availability, including same-release additions.
func (c *Catalog) RuntimeRelease(provider string) *sandbox.RuntimeRelease {
	if c == nil || c.nodes == nil || !c.nodes.available(provider) {
		return nil
	}
	release := c.nodes.releases[provider]
	release.Artifacts = maps.Clone(release.Artifacts)
	return &release
}

func (c *Catalog) NodeInstallation() *NodeInstallation {
	if c == nil || c.nodes == nil {
		return nil
	}
	result := &NodeInstallation{InstallerSHA256: c.nodes.digest, RuntimeReleases: map[string]sandbox.RuntimeRelease{}}
	for provider := range c.nodes.releases {
		if release := c.RuntimeRelease(provider); release != nil {
			result.RuntimeReleases[provider] = *release
		}
	}
	return result
}

func (c *Catalog) ServeNodeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		v1.WriteHTTPError(w, 405, "", "Method not allowed")
		return
	}
	notFound := func() { v1.WriteHTTPError(w, 404, "", "404 page not found") }
	if c == nil || c.nodes == nil || !strings.HasPrefix(r.URL.Path, nodeInstallPath) || r.URL.EscapedPath() != r.URL.Path {
		notFound()
		return
	}
	p := c.nodes
	name := strings.TrimPrefix(r.URL.Path, nodeInstallPath)
	prefix := p.prefix
	manifest := p.manifest
	if strings.HasPrefix(name, "releases/") {
		parts := strings.SplitN(name, "/", 3)
		if len(parts) != 3 || !payloadRevision.MatchString(parts[1]) {
			notFound()
			return
		}
		prefix, name = "releases/"+parts[1]+"/", parts[2]
		if prefix != p.prefix {
			var err error
			manifest, err = p.readNodeManifest(prefix)
			if err != nil {
				notFound()
				return
			}
		}
	}
	allowed := nodePayloadFiles[name]
	if !allowed && strings.HasPrefix(name, "artifacts/") {
		filename := strings.TrimPrefix(name, "artifacts/")
		for logical, entry := range manifest.Artifacts {
			if p.allowed[logical] && nodeArtifactName.MatchString(filename) && entry.Filename == filename {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		notFound()
		return
	}
	f, err := p.root.Open(prefix + name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && strings.HasPrefix(name, "artifacts/") {
			if target := manifest.artifactURL(strings.TrimPrefix(name, "artifacts/")); target != "" {
				http.Redirect(w, r, target, http.StatusTemporaryRedirect)
				return
			}
		}
		notFound()
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		notFound()
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(&downloadResponse{ResponseWriter: w}, r, info.Name(), info.ModTime(), f)
}
