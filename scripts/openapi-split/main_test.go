package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSeparatePublicAndAdministrationRoots(t *testing.T) {
	d := t.TempDir()
	input := filepath.Join(d, "combined.yaml")
	project := filepath.Join(d, "project.yaml")
	manager := filepath.Join(d, "manager.yaml")
	raw := `swagger: "2.0"
basePath: /v1
info:
  title: Agents API
  description: Project resources
paths:
  /agents/sessions:
    get:
      responses:
        "200":
          schema:
            $ref: '#/definitions/Session'
  /core/v1/sandbox/nodes:
    get:
      responses:
        "200":
          schema:
            $ref: '#/definitions/Node'
definitions:
  Session:
    properties:
      error:
        $ref: '#/definitions/Error'
  Node:
    properties:
      error:
        $ref: '#/definitions/Error'
  Error:
    type: object
`
	if err := os.WriteFile(input, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(input, project, manager); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ path, base, own, other, definition, absent string }{{project, "/v1", "/agents/sessions", "/core/v1/sandbox/nodes", "Session", "Node"}, {manager, "/", "/core/v1/sandbox/nodes", "/agents/sessions", "Node", "Session"}} {
		raw, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatal(err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		root := doc.Content[0]
		if field(root, "basePath").Value != check.base || field(field(root, "paths"), check.own) == nil || field(field(root, "paths"), check.other) != nil {
			t.Fatal("mixed URL roots")
		}
		defs := field(root, "definitions")
		if field(defs, check.definition) == nil || field(defs, "Error") == nil || field(defs, check.absent) != nil {
			t.Fatal("reference closure was not preserved")
		}
	}
}
