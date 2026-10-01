package agent_test

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This inventory makes a newly introduced interface require a deliberate role
// decision. Each adapter's compile assertions then enforce its actual methods.
func TestPublicHarnessContractDeclarations(t *testing.T) {
	roles := map[string][]string{
		"Executor":                 {"executor"},
		"Turn":                     {"session"},
		"Session":                  {"session"},
		"DurableSteerer":           {"session"},
		"Steerer":                  {"session"},
		"FunctionResultSubmitter":  {"session"},
		"PermissionResponder":      {"session"},
		"UserChoiceResponder":      {"session"},
		"WorkspaceReader":          {"executor", "prepared", "session"},
		"WorkspaceDirectoryLister": {"executor", "prepared", "session"},
		"WorkspaceWriter":          {"executor", "prepared", "session"},
		"Prepared":                 {"prepared"},
		"PreparedCancellation":     {"prepared"},
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(node ast.Node) bool {
			spec, ok := node.(*ast.TypeSpec)
			if !ok || !spec.Name.IsExported() {
				return true
			}
			if _, ok := spec.Type.(*ast.InterfaceType); ok {
				found[spec.Name.Name] = true
			}
			return true
		})
	}
	if len(found) != len(roles) {
		t.Fatal("classify every public Harness interface in the role contract", found)
	}
	for name := range found {
		if _, ok := roles[name]; !ok {
			t.Fatalf("unclassified interface %s", name)
		}
	}
	data, err := os.ReadFile("../../../../internal/harnessconfig/builtin/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog []struct {
		Configuration string `json:"configuration"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, entry := range catalog {
		t.Run(entry.Configuration, func(t *testing.T) {
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(entry.Configuration, "contracts.go"), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			assertions := map[string]map[string]bool{}
			ast.Inspect(f, func(node ast.Node) bool {
				spec, ok := node.(*ast.ValueSpec)
				if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "_" || len(spec.Values) != 1 {
					return true
				}
				contract, ok := spec.Type.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := contract.X.(*ast.Ident)
				if !ok || pkg.Name != "agent" {
					return true
				}
				call, ok := spec.Values[0].(*ast.CallExpr)
				if !ok {
					t.Fatal("contract assertion must identify its concrete owner")
				}
				paren, ok := call.Fun.(*ast.ParenExpr)
				if !ok {
					t.Fatal("expected pointer assertion")
				}
				pointer, ok := paren.X.(*ast.StarExpr)
				if !ok {
					t.Fatal("expected pointer owner")
				}
				owner, ok := pointer.X.(*ast.Ident)
				if !ok {
					t.Fatal("expected local concrete owner")
				}
				if assertions[contract.Sel.Name] == nil {
					assertions[contract.Sel.Name] = map[string]bool{}
				}
				assertions[contract.Sel.Name][strings.ToLower(owner.Name)] = true
				return true
			})
			for name, owners := range roles {
				want := map[string]bool{}
				for _, owner := range owners {
					want[owner] = true
				}
				if !reflect.DeepEqual(assertions[name], want) {
					t.Errorf("%s needs explicit compile assertions on %v; got %v", name, want, assertions[name])
				}
			}
			// Each discovered Runtime decides its agent-host view explicitly, even when it declares none.
			declaration, err := parser.ParseFile(token.NewFileSet(), filepath.Join(entry.Configuration, "declaration.go"), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			runtimes := 0
			ast.Inspect(declaration, func(node ast.Node) bool {
				literal, ok := node.(*ast.CompositeLit)
				if !ok {
					return true
				}
				selector, ok := literal.Type.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "Runtime" {
					return true
				}
				runtimes++
				for _, element := range literal.Elts {
					if field, ok := element.(*ast.KeyValueExpr); ok && fmt.Sprint(field.Key) == "View" {
						return true
					}
				}
				t.Error("agent.Runtime literal must set View explicitly")
				return true
			})
			if runtimes == 0 {
				t.Error("declaration.go constructs no agent.Runtime")
			}
		})
	}
}
