// Package agentplugin validates the supported inert Agents API Plugin format.
package agentplugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentbundle"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
)

var ErrInvalid = errors.New("invalid or unsupported Plugin bundle")

type Metadata struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Skill identifies a validated root inside its preserved Plugin package.
type Skill struct {
	Metadata     agentskill.Metadata `json:"metadata"`
	RelativeRoot string              `json:"relative_root"`
}

type Bundle struct {
	Metadata Metadata
	Files    []agentbundle.File
	Skills   []Skill
	MCP      []MCPServer
}

// Read validates the archive and its declared identity without native loading.
func Read(archive []byte, expected Metadata) (Bundle, error) {
	files, err := agentbundle.Read(archive)
	if err != nil {
		return Bundle{}, ErrInvalid
	}
	bundle, err := Inspect(files)
	if err != nil || bundle.Metadata != expected {
		return Bundle{}, ErrInvalid
	}
	return bundle, nil
}

// Inspect consumes files already bounded and rooted by the archive reader or
// Runtime snapshotter. It never executes or imports the native Plugin manifest.
func Inspect(files []agentbundle.File) (Bundle, error) {
	members := make(map[string][]byte, len(files))
	for _, file := range files {
		if _, exists := members[file.Path]; exists {
			return Bundle{}, ErrInvalid
		}
		members[file.Path] = file.Data
	}
	var manifest struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Version     string          `json:"version"`
		Skills      json.RawMessage `json:"skills"`
		MCP         json.RawMessage `json:"mcpServers"`
		Author      json.RawMessage `json:"author"`
		Homepage    string          `json:"homepage"`
		Repository  string          `json:"repository"`
		License     string          `json:"license"`
		Keywords    []string        `json:"keywords"`
		Interface   json.RawMessage `json:"interface"`
	}
	if decodeObject(members[".codex-plugin/plugin.json"], &manifest) != nil || manifest.Name == "" || manifest.Description == "" {
		return Bundle{}, ErrInvalid
	}
	roots, err := skillRoots(manifest.Skills)
	if err != nil {
		return Bundle{}, ErrInvalid
	}
	servers, err := readMCP(manifest.MCP, members)
	if err != nil {
		return Bundle{}, ErrInvalid
	}
	result := Bundle{Metadata: Metadata{Type: "inline", Name: manifest.Name, Description: manifest.Description}, Files: files, MCP: servers}
	seen := map[string]bool{}
	for _, file := range files {
		if path.Base(file.Path) != "SKILL.md" {
			continue
		}
		root := path.Dir(file.Path)
		selected := false
		for _, declared := range roots {
			selected = selected || strings.HasPrefix(file.Path, declared+"/")
		}
		if !selected {
			continue
		}
		metadata, err := agentskill.InspectManifest(file.Data)
		if err != nil || seen[metadata.Name] {
			return Bundle{}, ErrInvalid
		}
		seen[metadata.Name] = true
		result.Skills = append(result.Skills, Skill{Metadata: metadata, RelativeRoot: root})
	}
	if (len(result.Skills) == 0 && (len(roots) != 0 || len(result.MCP) == 0)) || len(result.Skills) > 50 {
		return Bundle{}, ErrInvalid
	}
	sort.Slice(result.Skills, func(i, j int) bool { return result.Skills[i].RelativeRoot < result.Skills[j].RelativeRoot })
	return result, nil
}

func skillRoots(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var roots []string
	var single string
	if json.Unmarshal(raw, &single) == nil {
		roots = []string{single}
	} else if json.Unmarshal(raw, &roots) != nil {
		return nil, ErrInvalid
	}
	if len(roots) == 0 || len(roots) > 50 {
		return nil, ErrInvalid
	}
	for i, root := range roots {
		root, err := relativeDeclaration(root)
		if err != nil {
			return nil, err
		}
		roots[i] = root
	}
	return roots, nil
}

func relativeDeclaration(value string) (string, error) {
	if !strings.HasPrefix(value, "./") || strings.ContainsAny(value, "\\\x00\r\n") {
		return "", ErrInvalid
	}
	value = strings.TrimSuffix(strings.TrimPrefix(value, "./"), "/")
	if value == "" || value == "." || value == ".." || strings.HasPrefix(value, "../") || path.Clean(value) != value || path.IsAbs(value) {
		return "", ErrInvalid
	}
	return value, nil
}

func decodeObject(body []byte, output any) error {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || len(body) > 256<<10 || body[0] != '{' {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(output) != nil {
		return ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}
