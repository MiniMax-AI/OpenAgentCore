// Command openapi-split emits internal contracts and exports Go definitions
// for the public contract extension overlay and the Core client types.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	projectSurface = iota
	coreSurface
	runtimeSurface
)

func main() {
	check := flag.Bool("check", false, "check generated internal contracts without changing them")
	flag.Parse()
	if flag.NArg() != 5 {
		fmt.Fprintln(os.Stderr, "usage: openapi-split [--check] INPUT EXTENSIONS_OUTPUT CORE_JSON_OUTPUT CORE_OUTPUT RUNTIME_OUTPUT")
		os.Exit(1)
	}
	if err := run(flag.Arg(0), flag.Arg(1), flag.Arg(2), flag.Arg(3), flag.Arg(4), *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run exports Go-owned definitions for the public extension overlay and the
// Core client types, and emits the two internal contracts. Public operations
// come only from official OpenAPI.
func run(input, extensionOutput, coreJSONOutput, coreOutput, runtimeOutput string, check bool) error {
	raw, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	var source yaml.Node
	if err := yaml.Unmarshal(raw, &source); err != nil {
		return err
	}
	if err := writeJSON(field(source.Content[0], "definitions"), extensionOutput); err != nil {
		return err
	}
	for _, output := range []struct {
		path, title, description string
		surface                  int
		// jsonOutput, when set, receives the document as JSON.
		jsonOutput string
	}{
		{coreOutput, "OpenAgentCore Core API", "Deployment and operations routes under /core/v1 for Core Web's server and operator scripts. Every operation requires the Core key; Project API keys and machine credentials are not accepted.", coreSurface, coreJSONOutput},
		{runtimeOutput, "OpenAgentCore Machine Connections", "Machine connection routes under /api/v1. Sandbox nodes authenticate with a one-use enrollment token or their node credential; Project API keys and the Core key are not accepted. See each operation's security requirements.", runtimeSurface, ""},
	} {
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return err
		}
		root := doc.Content[0]
		filterPaths(field(root, "paths"), output.surface)
		field(root, "basePath").Value = "/"
		field(field(root, "info"), "title").Value = output.title
		field(field(root, "info"), "description").Value = output.description
		pruneDefinitions(root)
		pruneSecurityDefinitions(root)
		var buf bytes.Buffer
		encoder := yaml.NewEncoder(&buf)
		encoder.SetIndent(2)
		if err := encoder.Encode(&doc); err != nil {
			return err
		}
		if err := encoder.Close(); err != nil {
			return err
		}
		if err := writeGenerated(output.path, buf.Bytes(), check); err != nil {
			return err
		}
		if output.jsonOutput != "" {
			if err := writeJSON(root, output.jsonOutput); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeJSON writes a YAML node as JSON, which scripts/generate-public-api.py
// reads without a YAML parser.
func writeJSON(node *yaml.Node, path string) error {
	var value map[string]any
	if err := node.Decode(&value); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0644)
}
func field(n *yaml.Node, key string) *yaml.Node {
	if n != nil {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
	}
	return nil
}
func surface(path string) int {
	switch {
	case strings.HasPrefix(path, "/core/v1/"):
		return coreSurface
	case strings.HasPrefix(path, "/api/v1/"):
		return runtimeSurface
	}
	return projectSurface
}
func filterPaths(n *yaml.Node, want int) {
	kept := make([]*yaml.Node, 0, len(n.Content))
	for i := 0; i < len(n.Content); i += 2 {
		if surface(n.Content[i].Value) == want {
			kept = append(kept, n.Content[i], n.Content[i+1])
		}
	}
	n.Content = kept
}
func pruneDefinitions(root *yaml.Node) {
	definitions := field(root, "definitions")
	if definitions == nil {
		return
	}
	needed := map[string]bool{}
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Value == "$ref" && strings.HasPrefix(v.Value, "#/definitions/") {
					name := strings.TrimPrefix(v.Value, "#/definitions/")
					if !needed[name] {
						needed[name] = true
						walk(field(definitions, name))
					}
				}
			}
		}
		for _, child := range n.Content {
			walk(child)
		}
	}
	walk(field(root, "paths"))
	kept := make([]*yaml.Node, 0, len(definitions.Content))
	for i := 0; i < len(definitions.Content); i += 2 {
		if needed[definitions.Content[i].Value] {
			kept = append(kept, definitions.Content[i], definitions.Content[i+1])
		}
	}
	definitions.Content = kept
}

// pruneSecurityDefinitions keeps only the schemes that the document's operations require.
func pruneSecurityDefinitions(root *yaml.Node) {
	schemes, paths := field(root, "securityDefinitions"), field(root, "paths")
	if schemes == nil || paths == nil {
		return
	}
	used := map[string]bool{}
	for i := 1; i < len(paths.Content); i += 2 {
		for j := 1; j < len(paths.Content[i].Content); j += 2 {
			if requirements := field(paths.Content[i].Content[j], "security"); requirements != nil {
				for _, requirement := range requirements.Content {
					for k := 0; k < len(requirement.Content); k += 2 {
						used[requirement.Content[k].Value] = true
					}
				}
			}
		}
	}
	kept := make([]*yaml.Node, 0, len(schemes.Content))
	for i := 0; i+1 < len(schemes.Content); i += 2 {
		if used[schemes.Content[i].Value] {
			kept = append(kept, schemes.Content[i], schemes.Content[i+1])
		}
	}
	schemes.Content = kept
}

func writeGenerated(path string, data []byte, check bool) error {
	if check {
		existing, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, data) {
			return fmt.Errorf("%s is stale; run make openapi", path)
		}
		return nil
	}
	return os.WriteFile(path, data, 0644)
}
