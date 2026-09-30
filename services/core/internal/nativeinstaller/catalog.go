// Package nativeinstaller serves qualified native distributions. It has no
// execution responsibilities; every installed daemon uses the common protocol.
package nativeinstaller

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

//go:embed assets/*
var bootstrap embed.FS

type Artifact struct {
	SHA256 string `json:"sha256"`
	URL    string `json:"url,omitempty"`
}

type Catalog struct {
	Version         string              `json:"version"`
	ProtocolVersion string              `json:"protocol_version"`
	Artifacts       map[string]Artifact `json:"artifacts"`
	directory       string
	local           map[string]bool
}

var checksum = regexp.MustCompile(`^[0-9a-f]{64}$`)

var platformName = regexp.MustCompile(`^(linux|darwin|windows)-(amd64|arm64)$`)

// Load checks the matched catalog without downloading execution payloads. Local
// offline archives are verified once; the directory stays immutable while serving.
func Load(directory, version string) (*Catalog, error) {
	raw, err := os.ReadFile(filepath.Join(directory, "catalog.json"))
	if err != nil {
		return nil, err
	}
	var c Catalog
	if json.Unmarshal(raw, &c) != nil || c.Version != version || !proto.VersionCompatible(c.ProtocolVersion) || len(c.Artifacts) == 0 {
		return nil, errors.New("native installer catalog does not match this Core build and protocol")
	}
	c.local = make(map[string]bool)
	for platform, artifact := range c.Artifacts {
		if !platformName.MatchString(platform) || !checksum.MatchString(artifact.SHA256) {
			return nil, errors.New("invalid native installer platform")
		}
		if artifact.URL != "" {
			u, err := url.Parse(artifact.URL)
			filename := "oac-native-" + c.Version + "-" + platform + ".tar.gz"
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, "/"+filename) || strings.Contains(strings.ToLower(u.Path), "/latest/") {
				return nil, errors.New("invalid versioned native installer URL")
			}
		}
		file, err := os.Open(filepath.Join(directory, platform+".tar.gz"))
		if errors.Is(err, os.ErrNotExist) && artifact.URL != "" {
			continue
		}
		if err != nil {
			return nil, err
		}
		c.local[platform] = true
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		file.Close()
		if err != nil || artifact.SHA256 != hex.EncodeToString(hash.Sum(nil)) {
			return nil, errors.New("native installer archive checksum mismatch")
		}
	}
	c.directory = directory
	return &c, nil
}

func (c *Catalog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/agent-daemon/install/"+c.Version+"/")
	if strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	if name == "bootstrap.sh" || name == "bootstrap.ps1" {
		raw, _ := bootstrap.ReadFile("assets/" + name)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(raw)
		return
	}
	platform := strings.TrimSuffix(strings.TrimSuffix(name, ".sha256"), ".tar.gz")
	artifact, ok := c.Artifacts[platform]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if name == platform+".sha256" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, artifact.SHA256)
		return
	}
	if name != platform+".tar.gz" {
		http.NotFound(w, r)
		return
	}
	if c.local[platform] {
		http.ServeFile(w, r, filepath.Join(c.directory, name))
		return
	}
	http.Redirect(w, r, artifact.URL, http.StatusTemporaryRedirect)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func psQuote(s string) string    { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (c *Catalog) Commands(origin, authorization string) map[string]string {
	base := origin + "/api/v1/agent-daemon/install/" + c.Version
	// Hold the small bootstrap in memory so an interrupted fetch leaves no file.
	// Only execute a complete successful response; preserve interactive stdin.
	posix := `set -e; script=; for attempt in 1 2 3; do if script=$(curl -fsS --connect-timeout 15 --max-time 60 --max-filesize 1048576 ` + shellQuote(base+"/bootstrap.sh") + `); then break; fi; [ "$attempt" -lt 3 ] || exit 1; sleep "$attempt"; done; bash -c "$script" -- "$@"`
	return map[string]string{
		"posix":      "bash -c " + shellQuote(posix) + " -- " + shellQuote(base) + " " + shellQuote(authorization),
		"powershell": "& { $source=$null; for ($attempt=1; $attempt -le 3; $attempt++) { try { $source=(Invoke-WebRequest -UseBasicParsing " + psQuote(base+"/bootstrap.ps1") + " -TimeoutSec 60 -ErrorAction Stop).Content; break } catch { if ($attempt -eq 3) { throw }; Start-Sleep -Seconds $attempt } }; & ([scriptblock]::Create($source)) -Base " + psQuote(base) + " -Authorization " + psQuote(authorization) + " @args }",
	}
}
