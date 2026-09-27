// Command openapi-split separates the generated document by namespace: the
// application API (/v1), the Core API (/core/v1) and machine connections
// (/api/v1) each get their own document.
package main

import (
	"bytes"
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
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: openapi-split INPUT PROJECT_OUTPUT CORE_OUTPUT RUNTIME_OUTPUT")
		os.Exit(1)
	}
	if err := run(os.Args[1], os.Args[2], os.Args[3], os.Args[4]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(input, projectOutput, coreOutput, runtimeOutput string) error {
	raw, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	var project, core, runtime yaml.Node
	for _, doc := range []*yaml.Node{&project, &core, &runtime} {
		if err = yaml.Unmarshal(raw, doc); err != nil {
			return err
		}
	}
	p, m, rt := project.Content[0], core.Content[0], runtime.Content[0]
	filterPaths(field(p, "paths"), projectSurface)
	filterPaths(field(m, "paths"), coreSurface)
	filterPaths(field(rt, "paths"), runtimeSurface)
	field(m, "basePath").Value = "/"
	field(field(m, "info"), "title").Value = "OpenAgentCore Core API"
	field(field(m, "info"), "description").Value = "Deployment and operations routes under /core/v1 for Core Web's server and operator scripts. Every operation requires the Core key; Project API keys and machine credentials are not accepted."
	field(rt, "basePath").Value = "/"
	field(field(rt, "info"), "title").Value = "OpenAgentCore Machine Connections"
	field(field(rt, "info"), "description").Value = "Machine connection routes under /api/v1. Sandbox nodes authenticate with a one-use enrollment token or their node credential; Project API keys and the Core key are not accepted. See each operation's security requirements."
	// Retain exactly the definitions referenced by each surface, including shared
	// error DTOs. Follow nested references instead of duplicating the project schema.
	pruneDefinitions(p)
	pruneDefinitions(m)
	pruneDefinitions(rt)
	// Each document keeps only the security schemes its operations require.
	pruneSecurityDefinitions(p)
	pruneSecurityDefinitions(m)
	pruneSecurityDefinitions(rt)
	// Keep unrelated existing project definitions and the generator's formatting.
	// Only definitions exclusive to the Core or machine surfaces are removed.
	var original yaml.Node
	if err := yaml.Unmarshal(raw, &original); err != nil {
		return err
	}
	originalDefinitions := field(original.Content[0], "definitions")
	projectDefinitions := field(p, "definitions")
	if originalDefinitions != nil {
		kept := make([]*yaml.Node, 0, len(originalDefinitions.Content))
		for i := 0; i < len(originalDefinitions.Content); i += 2 {
			key := originalDefinitions.Content[i]
			if field(projectDefinitions, key.Value) != nil || field(field(m, "definitions"), key.Value) == nil && field(field(rt, "definitions"), key.Value) == nil {
				kept = append(kept, key, originalDefinitions.Content[i+1])
			}
		}
		projectDefinitions.Content = kept
	}
	for _, out := range []struct {
		path string
		doc  *yaml.Node
	}{{projectOutput, &project}, {coreOutput, &core}, {runtimeOutput, &runtime}} {
		if out.path == projectOutput {
			if err := os.WriteFile(out.path, preserveProjectFormatting(raw, original.Content[0], p), 0644); err != nil {
				return err
			}
			continue
		}
		var buf bytes.Buffer
		encoder := yaml.NewEncoder(&buf)
		encoder.SetIndent(2)
		if err := encoder.Encode(out.doc); err != nil {
			return err
		}
		if err := encoder.Close(); err != nil {
			return err
		}
		if err := os.WriteFile(out.path, buf.Bytes(), 0644); err != nil {
			return err
		}
	}
	return nil
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

// Preserve the generated project's lexical form while removing whole mappings.
// YAML node line locations avoid interpreting indentation or quoted path names.
func preserveProjectFormatting(raw []byte, original, filtered *yaml.Node) []byte {
	lines := bytes.SplitAfter(raw, []byte("\n"))
	removed := make([]bool, len(lines))
	for _, section := range []string{"paths", "definitions", "securityDefinitions"} {
		source, target := field(original, section), field(filtered, section)
		if source == nil {
			continue
		}
		end := len(lines)
		for i := 0; i+1 < len(original.Content); i += 2 {
			if original.Content[i].Value == section && i+2 < len(original.Content) {
				end = original.Content[i+2].Line - 1
				break
			}
		}
		for i := 0; i+1 < len(source.Content); i += 2 {
			key := source.Content[i]
			if field(target, key.Value) != nil {
				continue
			}
			stop := end
			if i+2 < len(source.Content) {
				stop = source.Content[i+2].Line - 1
			}
			for row := key.Line - 1; row < stop; row++ {
				removed[row] = true
			}
		}
	}
	var out bytes.Buffer
	for i, line := range lines {
		if !removed[i] {
			out.Write(line)
		}
	}
	return out.Bytes()
}
