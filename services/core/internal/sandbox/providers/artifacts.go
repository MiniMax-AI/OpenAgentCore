package providers

import (
	"fmt"
	"path"
	"regexp"

	"github.com/MiniMax-AI/OpenAgentCore/internal/providerassets"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

var nodeProgram = providerassets.Artifact{Path: "native/bin/oac-node", Suffix: "sandbox-node", Role: "node"}
var runtimeImage = providerassets.Artifact{Path: "images/runtime.tar.gz", Suffix: "runtime.tar.gz", Role: "image"}
var runtimePolicy = providerassets.Artifact{Path: "runtime/seccomp.json", Suffix: "seccomp.json", Role: "policy"}

// ArtifactCatalog projects registered node requirements for Web and installers.
func ArtifactCatalog() (map[string][]providerassets.Artifact, error) {
	result := map[string][]providerassets.Artifact{}
	paths := map[string]providerassets.Artifact{}
	suffixes := map[string]string{}
	for kind := range adapters {
		a, err := Lookup(kind)
		if err != nil {
			return nil, err
		}
		if a.Mode == "nodes" {
			for _, item := range a.NodeArtifacts {
				previous, exists := paths[item.Path]
				if exists && previous != item || suffixes[item.Suffix] != "" && suffixes[item.Suffix] != item.Path {
					return nil, fmt.Errorf("%w: conflicting node artifact declarations", providercontract.ErrContract)
				}
				paths[item.Path], suffixes[item.Suffix] = item, item.Path
			}

			result[kind] = append([]providerassets.Artifact(nil), a.NodeArtifacts...)
		}
	}
	return result, nil
}

var artifactPath = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`)
var artifactSuffix = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func validateNodeArtifacts(items []providerassets.Artifact) error {
	invalid := fmt.Errorf("%w: invalid node artifact declaration", providercontract.ErrContract)
	if len(items) == 0 {
		return invalid
	}
	seen := map[string]bool{}
	nodes := 0
	for _, item := range items {
		if !artifactPath.MatchString(item.Path) || path.Clean(item.Path) != item.Path || !artifactSuffix.MatchString(item.Suffix) || seen[item.Path] {
			return invalid
		}
		switch item.Role {
		case "node":
			nodes++
		case "runtime", "policy", "image":
		default:
			return invalid
		}
		seen[item.Path] = true
	}
	if nodes != 1 {
		return invalid
	}
	return nil
}
