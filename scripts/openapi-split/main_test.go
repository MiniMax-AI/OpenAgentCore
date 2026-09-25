package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSeparatesTheThreeNamespaces(t *testing.T) {
	d := t.TempDir()
	input := filepath.Join(d, "combined.yaml")
	project := filepath.Join(d, "project.yaml")
	core := filepath.Join(d, "core.yaml")
	machine := filepath.Join(d, "runtime.yaml")
	raw := `swagger: "2.0"
basePath: /v1
info:
  title: Agents API
  description: Project resources
paths:
  /agents/sessions:
    get:
      security:
        - BearerAuth: []
      responses:
        "200":
          schema:
            $ref: '#/definitions/Session'
  /core/v1/sandbox/nodes:
    get:
      security:
        - DeploymentAdminAuth: []
      responses:
        "200":
          schema:
            $ref: '#/definitions/Node'
  /api/v1/sandbox-node/identity:
    get:
      security:
        - NodeAuth: []
      responses:
        "200":
          schema:
            $ref: '#/definitions/NodeIdentity'
definitions:
  Session:
    properties:
      error:
        $ref: '#/definitions/Error'
  Node:
    properties:
      error:
        $ref: '#/definitions/Error'
  NodeIdentity:
    properties:
      error:
        $ref: '#/definitions/Error'
  Error:
    type: object
securityDefinitions:
  BearerAuth:
    type: apiKey
  DeploymentAdminAuth:
    type: apiKey
  NodeAuth:
    type: apiKey
`
	if err := os.WriteFile(input, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(input, project, core, machine); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ path, base, own, other, definition, absent string }{
		{project, "/v1", "/agents/sessions", "/core/v1/sandbox/nodes", "Session", "Node"},
		{project, "/v1", "/agents/sessions", "/api/v1/sandbox-node/identity", "Session", "NodeIdentity"},
		{core, "/", "/core/v1/sandbox/nodes", "/agents/sessions", "Node", "Session"},
		{core, "/", "/core/v1/sandbox/nodes", "/api/v1/sandbox-node/identity", "Node", "NodeIdentity"},
		{machine, "/", "/api/v1/sandbox-node/identity", "/core/v1/sandbox/nodes", "NodeIdentity", "Node"},
		{machine, "/", "/api/v1/sandbox-node/identity", "/agents/sessions", "NodeIdentity", "Session"},
	} {
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
	// Every document keeps only the schemes its operations use.
	for path, want := range map[string]string{project: "BearerAuth", core: "DeploymentAdminAuth", machine: "NodeAuth"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		var names []string
		schemes := field(doc.Content[0], "securityDefinitions")
		for i := 0; i < len(schemes.Content); i += 2 {
			names = append(names, schemes.Content[i].Value)
		}
		if got := strings.Join(names, " "); got != want {
			t.Fatalf("%s security definitions = %q, want %q", filepath.Base(path), got, want)
		}
	}
}
