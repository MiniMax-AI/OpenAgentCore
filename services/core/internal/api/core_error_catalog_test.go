package api

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Codes the error writers produce that never reach an administration caller.
// Everything else they produce is in the catalog or in the wire vocabulary of
// wire-semantics.md, which keeps its /v1 meaning on every route.
var nonAdministrationCodes = []string{
	// Session creation and input admission on /v1.
	"environment_input_cancelled", "environment_input_expired", "model_provider_required", "sandbox_nodes_preparing", "turn_conflict",
	// Machine routes for nodes and native installers.
	"installation_authorization_invalid", "installation_unavailable", "invalid_node_credential", "sandbox_node_address_mismatch",
}

// The shared catalog lists every code an administration caller (/core/v1 or
// the console's /core paths) can receive outside the wire vocabulary.
// core-errors.md documents and Web localizes exactly these codes.
func TestCoreErrorCatalog(t *testing.T) {
	raw, err := os.ReadFile("testdata/core-errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog []string
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	// One pass collects every string literal, which covers codes carried by
	// typed errors, and the literal codes passed to the error writers.
	literals, written := map[string]bool{}, map[string]bool{}
	files := token.NewFileSet()
	for _, root := range []string{"../../../../services", "../../../../contracts"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case entry.IsDir() && (entry.Name() == "node_modules" || entry.Name() == "testdata"):
				return filepath.SkipDir
			case entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go"):
				return nil
			}
			file, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.BasicLit:
					if value, err := strconv.Unquote(node.Value); node.Kind == token.STRING && err == nil {
						literals[value] = true
					}
				case *ast.CallExpr:
					if name, ok := node.Fun.(*ast.Ident); ok && len(node.Args) > 2 && slices.Contains([]string{"writeError", "writeAPIError", "writeCoreError", "consoleCoreError"}, name.Name) {
						if code, ok := node.Args[2].(*ast.BasicLit); ok {
							value, _ := strconv.Unquote(code.Value)
							written[value] = value != ""
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range slices.Concat(catalog, nonAdministrationCodes) {
		if !literals[code] {
			t.Errorf("code %q has no Go producer", code)
		}
	}
	wire := backtickedCodes(t, "../../../../contracts/agents-api/wire-semantics.md")
	for code, produced := range written {
		if produced && !slices.Contains(catalog, code) && !slices.Contains(nonAdministrationCodes, code) && !wire[code] {
			t.Errorf("produced code %q is missing from the catalog; list it in nonAdministrationCodes only if no administration caller can receive it", code)
		}
	}
	documented := documentedCoreErrorCodes(t, "../../../../contracts/agents-api/core-errors.md")
	slices.Sort(catalog)
	if !slices.Equal(documented, catalog) {
		t.Errorf("core-errors.md codes = %v, want shared catalog %v", documented, catalog)
	}
}

var backtickedCode = regexp.MustCompile("`([a-z_]+)`")

func backtickedCodes(t *testing.T, path string) map[string]bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, match := range backtickedCode.FindAllStringSubmatch(string(raw), -1) {
		codes[match[1]] = true
	}
	return codes
}

// documentedCoreErrorCodes reads the Code column of every error table before
// the diagnostics catalog, which is a separate vocabulary.
func documentedCoreErrorCodes(t *testing.T, path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text, _, _ := strings.Cut(string(raw), "## Diagnostic failure categories")
	codes := map[string]bool{}
	column := -1
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "|") {
			column = -1
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if column < 0 {
			column = slices.IndexFunc(cells, func(cell string) bool { return strings.TrimSpace(cell) == "Code" })
			continue
		}
		if column < len(cells) {
			for _, match := range backtickedCode.FindAllStringSubmatch(cells[column], -1) {
				codes[match[1]] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(codes))
}
