// Command openapi-split separates Core administration from the project API.
package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: openapi-split INPUT PROJECT_OUTPUT MANAGER_OUTPUT")
		os.Exit(1)
	}
	if err := run(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(input, projectOutput, managerOutput string) error {
	raw, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	var project, manager yaml.Node
	if err = yaml.Unmarshal(raw, &project); err != nil {
		return err
	}
	if err = yaml.Unmarshal(raw, &manager); err != nil {
		return err
	}
	p, m := project.Content[0], manager.Content[0]
	filterPaths(field(p, "paths"), false)
	filterPaths(field(m, "paths"), true)
	field(m, "basePath").Value = "/"
	field(field(m, "info"), "title").Value = "Core Sandbox Manager"
	field(field(m, "info"), "description").Value = "Deployment administration and node enrollment. Uses separate administrator and node credentials; project API keys do not authorize these operations."
	// Retain exactly the definitions referenced by each surface, including shared
	// error DTOs. Follow nested references instead of duplicating the project schema.
	pruneDefinitions(p)
	pruneDefinitions(m)
	// Keep unrelated existing project definitions and the generator's formatting.
	// Only definitions exclusive to the management surface are removed.
	var original yaml.Node
	if err := yaml.Unmarshal(raw, &original); err != nil {
		return err
	}
	originalDefinitions := field(original.Content[0], "definitions")
	projectDefinitions, managerDefinitions := field(p, "definitions"), field(m, "definitions")
	if originalDefinitions != nil {
		kept := make([]*yaml.Node, 0, len(originalDefinitions.Content))
		for i := 0; i < len(originalDefinitions.Content); i += 2 {
			key := originalDefinitions.Content[i]
			if field(projectDefinitions, key.Value) != nil || field(managerDefinitions, key.Value) == nil {
				kept = append(kept, key, originalDefinitions.Content[i+1])
			}
		}
		projectDefinitions.Content = kept
	}
	for _, out := range []struct {
		path string
		doc  *yaml.Node
	}{{projectOutput, &project}, {managerOutput, &manager}} {
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
func filterPaths(n *yaml.Node, manager bool) {
	kept := make([]*yaml.Node, 0, len(n.Content))
	for i := 0; i < len(n.Content); i += 2 {
		if strings.HasPrefix(n.Content[i].Value, "/core/v1/") == manager {
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

// Preserve the generated project's lexical form while removing whole mappings.
// YAML node line locations avoid interpreting indentation or quoted path names.
func preserveProjectFormatting(raw []byte, original, filtered *yaml.Node) []byte {
	lines := bytes.SplitAfter(raw, []byte("\n"))
	removed := make([]bool, len(lines))
	for _, section := range []string{"paths", "definitions"} {
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
