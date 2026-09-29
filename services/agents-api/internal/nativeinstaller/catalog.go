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
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

//go:embed assets/*
var bootstrap embed.FS

type Artifact struct {
	SHA256 string `json:"sha256"`
}

type Catalog struct {
	Version         string              `json:"version"`
	ProtocolVersion string              `json:"protocol_version"`
	Artifacts       map[string]Artifact `json:"artifacts"`
	directory       string
}

var platformName = regexp.MustCompile(`^(linux|darwin|windows)-(amd64|arm64)$`)

// Load refuses a different build or protocol, and verifies all archives once
// before exposing them. The distribution directory is immutable while serving.
func Load(directory, version string) (*Catalog, error) {
	raw, err := os.ReadFile(filepath.Join(directory, "catalog.json"))
	if err != nil {
		return nil, err
	}
	var c Catalog
	if json.Unmarshal(raw, &c) != nil || c.Version != version || !proto.VersionCompatible(c.ProtocolVersion) || len(c.Artifacts) == 0 {
		return nil, errors.New("native installer catalog does not match this Core build and protocol")
	}
	for platform, artifact := range c.Artifacts {
		if !platformName.MatchString(platform) {
			return nil, errors.New("invalid native installer platform")
		}
		file, err := os.Open(filepath.Join(directory, platform+".tar.gz"))
		if err != nil {
			return nil, err
		}
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
	http.ServeFile(w, r, filepath.Join(c.directory, name))
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func psQuote(s string) string    { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (c *Catalog) Commands(origin, authorization string) map[string]string {
	base := origin + "/api/v1/agent-daemon/install/" + c.Version
	// Download to a private temporary file so failure cannot become an empty,
	// successful shell program. Keep stdin available for installer interaction.
	posix := "set -e; f=$(mktemp); trap 'rm -f \"$f\"' EXIT; curl -fsS " + shellQuote(base+"/bootstrap.sh") + " -o \"$f\"; bash \"$f\" \"$@\""
	return map[string]string{
		"posix":      "bash -c " + shellQuote(posix) + " -- " + shellQuote(base) + " " + shellQuote(authorization),
		"powershell": "& ([scriptblock]::Create((Invoke-WebRequest -UseBasicParsing " + psQuote(base+"/bootstrap.ps1") + " -ErrorAction Stop).Content)) -Base " + psQuote(base) + " -Authorization " + psQuote(authorization),
	}
}
