// Package agentskill validates inert Skill bundles without native loading rules.
package agentskill

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"

	"gopkg.in/yaml.v3"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentbundle"
)

const (
	MaxArchiveBytes  = agentbundle.MaxArchiveBytes
	MaxExpandedBytes = agentbundle.MaxExpandedBytes
	MaxFiles         = agentbundle.MaxFiles
)

var ErrInvalid = errors.New("invalid or unsupported Skill bundle")
var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

// Read validates the full archive before exposing any files for installation.
func Read(archive []byte, expected proto.SkillMetadata) ([]agentbundle.File, error) {
	if expected.Type != "inline" || !namePattern.MatchString(expected.Name) || len(expected.Name) > 64 || expected.Description == "" || !utf8.ValidString(expected.Description) {
		return nil, ErrInvalid
	}
	files, err := agentbundle.Read(archive)
	if err != nil {
		return nil, ErrInvalid
	}
	manifest := false
	for i := range files {
		if strings.HasPrefix(files[i].Path, "SKILL.md/") {
			return nil, ErrInvalid
		}
		if strings.EqualFold(files[i].Path, "SKILL.md") {
			if manifest || ValidateManifest(files[i].Data, expected) != nil {
				return nil, ErrInvalid
			}
			manifest = true
			files[i].Path = "SKILL.md"
		}
	}
	if !manifest {
		return nil, ErrInvalid
	}
	return files, nil
}

// ValidateManifest accepts portable descriptive metadata, not native activation controls.
func ValidateManifest(body []byte, expected proto.SkillMetadata) error {
	actual, err := InspectManifest(body)
	if err != nil || actual != expected {
		return ErrInvalid
	}
	return nil
}

// InspectManifest shares the portable parser with Runtime directory discovery.
func InspectManifest(body []byte) (proto.SkillMetadata, error) {
	metadata, err := manifestMetadata(body)
	if err != nil || !namePattern.MatchString(metadata.Name) || len(metadata.Name) > 64 || metadata.Description == "" {
		return proto.SkillMetadata{}, ErrInvalid
	}
	return metadata, nil
}

func manifestMetadata(body []byte) (proto.SkillMetadata, error) {
	if len(body) > 256<<10 || !utf8.Valid(body) {
		return proto.SkillMetadata{}, ErrInvalid
	}
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return proto.SkillMetadata{}, ErrInvalid
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return proto.SkillMetadata{}, ErrInvalid
	}
	end += 4
	tail := text[end+4:]
	if tail != "" && !strings.HasPrefix(tail, "\n") {
		return proto.SkillMetadata{}, ErrInvalid
	}
	decoder := yaml.NewDecoder(strings.NewReader(text[4:end]))
	var document yaml.Node
	if decoder.Decode(&document) != nil || len(document.Content) != 1 {
		return proto.SkillMetadata{}, ErrInvalid
	}
	var extra yaml.Node
	if decoder.Decode(&extra) != io.EOF {
		return proto.SkillMetadata{}, ErrInvalid
	}
	node := document.Content[0]
	if node.Kind != yaml.MappingNode {
		return proto.SkillMetadata{}, ErrInvalid
	}
	fields := map[string]*yaml.Node{}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || fields[key.Value] != nil {
			return proto.SkillMetadata{}, ErrInvalid
		}
		fields[key.Value] = value
		switch key.Value {
		case "name", "description", "license", "compatibility":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return proto.SkillMetadata{}, ErrInvalid
			}
		case "metadata":
			var metadata map[string]string
			if value.Kind != yaml.MappingNode || value.Decode(&metadata) != nil {
				return proto.SkillMetadata{}, ErrInvalid
			}
		default:
			return proto.SkillMetadata{}, ErrInvalid
		}
	}
	if fields["name"] == nil || fields["description"] == nil {
		return proto.SkillMetadata{}, ErrInvalid
	}
	return proto.SkillMetadata{Type: "inline", Name: fields["name"].Value, Description: fields["description"].Value}, nil
}
