package api

// These checks keep the published responses and error codes tied to the code
// that writes them. They read source only; no handler runs. See
// contracts/agents-api/error-codes.md for the registry they compare against.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const conformanceRepoRoot = "../../../.."

// statusValues maps the net/http constants this repository writes. An unknown
// constant fails the check instead of being silently ignored.
var statusValues = map[string]int{
	"StatusOK": 200, "StatusCreated": 201, "StatusAccepted": 202, "StatusNoContent": 204,
	"StatusBadRequest": 400, "StatusUnauthorized": 401, "StatusForbidden": 403, "StatusNotFound": 404,
	"StatusMethodNotAllowed": 405, "StatusConflict": 409, "StatusRequestEntityTooLarge": 413,
	"StatusUnsupportedMediaType": 415, "StatusUpgradeRequired": 426, "StatusTooManyRequests": 429,
	"StatusInternalServerError": 500, "StatusBadGateway": 502, "StatusServiceUnavailable": 503,
	"StatusGatewayTimeout": 504,
}

type sourcePackage struct {
	fset  *token.FileSet
	files []*ast.File
	funcs map[string]*ast.FuncDecl // "Name" or "Receiver.Name"
}

func parseSourcePackage(t *testing.T, dir string) sourcePackage {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	result := sourcePackage{fset: fset, funcs: map[string]*ast.FuncDecl{}}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		result.files = append(result.files, file)
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				result.funcs[funcKey(fn)] = fn
			}
		}
	}
	return result
}

func funcKey(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if ident, ok := typ.(*ast.Ident); ok {
		return ident.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// statusConstant returns the value of an http.StatusX selector.
func statusConstant(t *testing.T, expr ast.Expr) (int, bool) {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || pkg.Name != "http" || !strings.HasPrefix(selector.Sel.Name, "Status") || selector.Sel.Name == "StatusText" {
		return 0, false
	}
	value, known := statusValues[selector.Sel.Name]
	if !known {
		t.Fatalf("add http.%s to statusValues", selector.Sel.Name)
	}
	return value, true
}

// errorWriters take the HTTP status as their second argument.
var errorWriters = map[string]bool{"writeError": true, "writeAPIError": true, "writeCoreError": true}

// statusArgument reads a status argument: an http.StatusX constant or an
// integer literal in the HTTP status range, such as writeError(w, 503, ...).
func statusArgument(t *testing.T, expr ast.Expr) (int, bool) {
	if status, ok := statusConstant(t, expr); ok {
		return status, true
	}
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return 0, false
	}
	value, err := strconv.Atoi(literal.Value)
	return value, err == nil && value >= 100 && value <= 599
}

func stringLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

// writtenStatuses collects the http.StatusX constants a function body uses as
// values. Comparisons and switch cases read a status rather than write one.
func writtenStatuses(t *testing.T, body ast.Node) map[int]bool {
	skip := map[ast.Expr]bool{}
	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.BinaryExpr:
			if n.Op == token.EQL || n.Op == token.NEQ {
				skip[n.X], skip[n.Y] = true, true
			}
		case *ast.CaseClause:
			for _, expr := range n.List {
				skip[expr] = true
			}
		}
		return true
	})
	statuses := map[int]bool{}
	ast.Inspect(body, func(node ast.Node) bool {
		// A literal status is counted only where an error writer takes it.
		if call, ok := node.(*ast.CallExpr); ok && len(call.Args) > 1 {
			if ident, ok := call.Fun.(*ast.Ident); ok && errorWriters[ident.Name] {
				if literal, ok := call.Args[1].(*ast.BasicLit); ok {
					if status, ok := statusArgument(t, literal); ok {
						statuses[status] = true
					}
				}
			}
		}
		expr, ok := node.(ast.Expr)
		if !ok || skip[expr] {
			return true
		}
		if status, ok := statusConstant(t, expr); ok {
			statuses[status] = true
			return false
		}
		return true
	})
	return statuses
}

// references lists package functions and methods a function calls or passes
// as a value. Without type information a method resolves only through the
// function's own receiver or a Handler named h; w.WriteHeader on an interface
// and field calls such as h.store.Get stay unresolved.
func (p sourcePackage) references(fn *ast.FuncDecl) []string {
	receivers := map[string]string{"h": "Handler"}
	if fn.Recv != nil && len(fn.Recv.List) > 0 && len(fn.Recv.List[0].Names) > 0 {
		if receiver, _, ok := strings.Cut(funcKey(fn), "."); ok {
			receivers[fn.Recv.List[0].Names[0].Name] = receiver
		}
	}
	var out []string
	selected := map[*ast.Ident]bool{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			selected[n.Sel] = true
			if ident, ok := n.X.(*ast.Ident); ok {
				if receiver, ok := receivers[ident.Name]; ok {
					if _, exists := p.funcs[receiver+"."+n.Sel.Name]; exists {
						out = append(out, receiver+"."+n.Sel.Name)
					}
				}
				return false
			}
		case *ast.Ident:
			if selected[n] {
				return true
			}
			if fn, ok := p.funcs[n.Name]; ok && fn.Recv == nil {
				out = append(out, n.Name)
			}
		}
		return true
	})
	return out
}

type operation struct {
	handler, method, path string
	declared              map[int]bool
	successes, failures   int
}

var (
	routerAnnotation = regexp.MustCompile(`^@Router\s+(\S+)\s+\[(\w+)\]`)
	statusAnnotation = regexp.MustCompile(`^@(Success|Failure)\s+([0-9,]+)(\s|$)`)
)

func annotatedOperations(p sourcePackage) []operation {
	var out []operation
	for key, fn := range p.funcs {
		if fn.Doc == nil {
			continue
		}
		op := operation{handler: key, declared: map[int]bool{}}
		for _, comment := range fn.Doc.List {
			line := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if match := routerAnnotation.FindStringSubmatch(line); match != nil {
				op.path, op.method = match[1], strings.ToUpper(match[2])
			}
			if match := statusAnnotation.FindStringSubmatch(line); match != nil {
				for _, value := range strings.Split(match[2], ",") {
					status, _ := strconv.Atoi(value)
					op.declared[status] = true
				}
				if match[1] == "Success" {
					op.successes++
				} else {
					op.failures++
				}
			}
		}
		if op.path != "" {
			out = append(out, op)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path+out[i].method < out[j].path+out[j].method })
	return out
}

// storeDispatcher maps stored-error sentinels to many statuses. Which sentinel
// a store call returns is not visible statically, so an operation reaching it
// is required to declare only what every lookup can produce: 500 for an
// unknown persistence failure and, when the path names a resource, 404. Any
// status the dispatcher writes is allowed as a declaration.
const storeDispatcher = "writeStoreError"

// middlewareStatuses are written before an operation's handler runs. Paths
// are relative to the contract base path, so /v1 operations have no prefix.
func middlewareStatuses(path string) []int {
	switch {
	case strings.HasPrefix(path, "/core/v1/"):
		return []int{401} // invalid_admin_key
	case strings.HasPrefix(path, "/api/v1/"):
		return []int{401} // enrollment token or node credential
	case strings.HasPrefix(path, "/files") || strings.HasPrefix(path, "/skills"):
		// authenticateProject: invalid_api_key or null code; authentication_unavailable.
		return []int{401, 503}
	default:
		// authenticate adds the OpenAI-Beta check: 400 invalid_beta.
		return []int{400, 401, 503}
	}
}

// reachableStatuses maps each status a handler can write to the first function
// found writing it, so a failure names where the status comes from, and
// reports whether the handler reaches the stored-error dispatcher.
func (p sourcePackage) reachableStatuses(t *testing.T, handler string) (map[int]string, bool) {
	statuses := map[int]string{}
	usesStore := false
	seen := map[string]bool{}
	queue := []string{handler}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if seen[key] {
			continue
		}
		seen[key] = true
		if key == storeDispatcher {
			usesStore = true
			continue
		}
		fn := p.funcs[key]
		if fn == nil || fn.Body == nil {
			continue
		}
		for status := range writtenStatuses(t, fn.Body) {
			if _, ok := statuses[status]; !ok {
				statuses[status] = key
			}
		}
		queue = append(queue, p.references(fn)...)
	}
	return statuses, usesStore
}

func sortedStatuses(values map[int]bool) []int {
	out := make([]int, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Ints(out)
	return out
}

// publishedOperations counts the operations of the three generated contracts.
func publishedOperations(t *testing.T) int {
	t.Helper()
	count := 0
	for _, name := range []string{"openapi.yaml", "core.openapi.yaml", "runtime.openapi.yaml"} {
		raw, err := os.ReadFile(filepath.Join(conformanceRepoRoot, "contracts/agents-api", name))
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Paths map[string]map[string]any `yaml:"paths"`
		}
		if err := yaml.Unmarshal(raw, &document); err != nil {
			t.Fatal(name, err)
		}
		for _, item := range document.Paths {
			for method := range item {
				switch method {
				case "get", "put", "post", "delete", "options", "head", "patch", "trace":
					count++
				}
			}
		}
	}
	return count
}

// withoutSuccess lists operations that never succeed, with the reason.
var withoutSuccess = map[string]string{
	// Every stored File has purpose user_data, whose download returns 400 as
	// the official API does; the operation exists only for route parity.
	"Handler.sourceFileContent": "user_data Files cannot be downloaded",
}

// TestAnnotatedResponsesCoverWrittenStatuses requires every published
// operation to declare a success and a failure response (G1), every status its
// handler, its own helpers or its middleware can write, and no failure status
// that none of them can write (G2).
func TestAnnotatedResponsesCoverWrittenStatuses(t *testing.T) {
	p := parseSourcePackage(t, ".")
	storeStatuses := writtenStatuses(t, p.funcs[storeDispatcher].Body)
	operations := annotatedOperations(p)
	if published := publishedOperations(t); len(operations) != published {
		t.Errorf("found %d annotated operations, but the contracts publish %d; run make openapi", len(operations), published)
	}
	for _, op := range operations {
		name := op.method + " " + op.path + " (" + op.handler + ")"
		if _, exempt := withoutSuccess[op.handler]; op.successes == 0 && !exempt {
			t.Errorf("%s declares no @Success response", name)
		}
		if op.failures == 0 {
			t.Errorf("%s declares no @Failure response", name)
		}
		required, usesStore := p.reachableStatuses(t, op.handler)
		possible := map[int]bool{}
		for status := range required {
			possible[status] = true
		}
		for _, status := range middlewareStatuses(op.path) {
			possible[status] = true
			if _, ok := required[status]; !ok {
				required[status] = "authentication middleware"
			}
		}
		if usesStore {
			for status := range storeStatuses {
				possible[status] = true
			}
			baseline := []int{500}
			if strings.Contains(op.path, "{") {
				baseline = append(baseline, 404)
			}
			for _, status := range baseline {
				if _, ok := required[status]; !ok {
					required[status] = storeDispatcher
				}
			}
		}
		var missing []string
		for status, source := range required {
			if !op.declared[status] {
				missing = append(missing, fmt.Sprintf("%d (from %s)", status, source))
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s can write %s but does not declare it (declares %v)", name, strings.Join(missing, ", "), sortedStatuses(op.declared))
		}
		var impossible []int
		for status := range op.declared {
			if status >= 400 && !possible[status] {
				impossible = append(impossible, status)
			}
		}
		sort.Ints(impossible)
		if len(impossible) > 0 {
			t.Errorf("%s declares %v, which neither its handler, its helpers nor its middleware can write", name, impossible)
		}
	}
}

// emittedCode is one (status, code) pair a writer call can produce. A status of
// 0 means the call passes a variable status.
type emittedCode struct {
	status int
	code   string
}

func (e emittedCode) String() string {
	if e.code == "" {
		return fmt.Sprintf("%d null", e.status)
	}
	return fmt.Sprintf("%d %s", e.status, e.code)
}

// emittedCodes collects (status, code) pairs from calls to the named writers,
// whose status and code are the arguments at the given positions. A code
// passed as a local variable resolves to the string literals assigned to it in
// the same function; an empty code is the null code.
func emittedCodes(t *testing.T, p sourcePackage, writers map[string][2]int) map[emittedCode][]string {
	out := map[emittedCode][]string{}
	for key, fn := range p.funcs {
		if fn.Body == nil {
			continue
		}
		if _, wrapper := writers[key]; wrapper {
			continue
		}
		assigned := map[string][]string{}
		// Variables assigned from a multi-value call, such as
		// status, code := mapAuthError(err), are covered by returnedCodes.
		fromCall := map[string]bool{}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if assign, ok := node.(*ast.AssignStmt); ok && len(assign.Rhs) == 1 && len(assign.Lhs) > 1 {
				if _, call := assign.Rhs[0].(*ast.CallExpr); call {
					for _, lhs := range assign.Lhs {
						if ident, ok := lhs.(*ast.Ident); ok {
							fromCall[ident.Name] = true
						}
					}
				}
			}
			if assign, ok := node.(*ast.AssignStmt); ok && len(assign.Lhs) == len(assign.Rhs) {
				for i, lhs := range assign.Lhs {
					if ident, ok := lhs.(*ast.Ident); ok {
						if value, ok := stringLiteral(assign.Rhs[i]); ok {
							assigned[ident.Name] = append(assigned[ident.Name], value)
						}
					}
				}
			}
			return true
		})
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			positions, ok := writers[ident.Name]
			if !ok || len(call.Args) <= positions[1] {
				return true
			}
			status, _ := statusArgument(t, call.Args[positions[0]])
			var codes []string
			if value, ok := stringLiteral(call.Args[positions[1]]); ok {
				codes = []string{value}
			} else if variable, ok := call.Args[positions[1]].(*ast.Ident); ok {
				codes = assigned[variable.Name]
				if len(codes) == 0 && fromCall[variable.Name] {
					return true
				}
				if len(codes) == 0 {
					t.Errorf("%s: cannot resolve code variable %s", p.fset.Position(call.Pos()), variable.Name)
				}
			} else if field, ok := call.Args[positions[1]].(*ast.SelectorExpr); ok && field.Sel.Name == "Code" && typedVariables(fn)[identName(field.X)] != "" {
				typ := typedVariables(fn)[identName(field.X)]
				codes = typedCodes[typ]
				if len(codes) == 0 {
					t.Errorf("%s: no %s{Code: ...} literals found for %s.Code", p.fset.Position(call.Pos()), typ, identName(field.X))
				}
			} else {
				t.Errorf("%s: code argument is neither a literal, a local variable nor a typed error's Code", p.fset.Position(call.Pos()))
			}
			for _, code := range codes {
				// An empty code is a null code; it is compared with the null rows.
				if status == 0 {
					t.Errorf("%s: code %s is written with a variable status", p.fset.Position(call.Pos()), code)
				}
				position := p.fset.Position(call.Pos())
				out[emittedCode{status, code}] = append(out[emittedCode{status, code}], filepath.Base(position.Filename)+":"+strconv.Itoa(position.Line))
			}
			return true
		})
	}
	return out
}

// typedCodes maps a validation error type to the codes its literals set. A
// writer that passes field.Code, for a variable declared as that type, can
// write any of them. Filled by collectTypedCodes before the registry check.
var typedCodes = map[string][]string{}

// typedErrorTypes are the error types whose Code member reaches an error writer,
// with the packages that construct them.
var typedErrorTypes = map[string]string{
	"AdminValidationError": "services/agents-api/internal/store",
	"ModelProviderError":   "contracts/agents-api/v1",
}

func collectTypedCodes(t *testing.T) {
	for typ, dir := range typedErrorTypes {
		p := parseSourcePackage(t, filepath.Join(conformanceRepoRoot, dir))
		seen := map[string]bool{}
		for _, file := range p.files {
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.CompositeLit)
				if !ok || identName(literal.Type) != typ {
					return true
				}
				for _, element := range literal.Elts {
					if pair, ok := element.(*ast.KeyValueExpr); ok && identName(pair.Key) == "Code" {
						code, ok := stringLiteral(pair.Value)
						if !ok {
							t.Errorf("%s: %s.Code is not a string literal", p.fset.Position(pair.Pos()), typ)
						} else if !seen[code] {
							seen[code] = true
							typedCodes[typ] = append(typedCodes[typ], code)
						}
					}
				}
				return true
			})
		}
		if len(typedCodes[typ]) == 0 {
			t.Fatalf("no %s literals in %s", typ, dir)
		}
	}
}

// identName is the name of an identifier or the selected name of pkg.Name.
func identName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.StarExpr:
		return identName(e.X)
	}
	return ""
}

// typedVariables maps variables declared as `var name *T` or `var name T` in a
// function to T, for the typed error types above.
func typedVariables(fn *ast.FuncDecl) map[string]string {
	out := map[string]string{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || spec.Type == nil {
			return true
		}
		if typ := identName(spec.Type); typedErrorTypes[typ] != "" {
			for _, name := range spec.Names {
				out[name.Name] = typ
			}
		}
		return true
	})
	return out
}

// returnedCodes collects functions that return (http.StatusX, "code").
func returnedCodes(t *testing.T, p sourcePackage, out map[emittedCode][]string) {
	for _, fn := range p.funcs {
		if fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			ret, ok := node.(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 2 {
				return true
			}
			status, ok := statusConstant(t, ret.Results[0])
			code, isString := stringLiteral(ret.Results[1])
			if ok && isString {
				position := p.fset.Position(ret.Pos())
				out[emittedCode{status, code}] = append(out[emittedCode{status, code}], filepath.Base(position.Filename)+":"+strconv.Itoa(position.Line))
			}
			return true
		})
	}
}

// streamErrorCodes collects v1.StreamError{Code: "..."} literals: error objects
// inside Session events, whose response status is already 200.
func streamErrorCodes(p sourcePackage) map[emittedCode][]string {
	out := map[emittedCode][]string{}
	for _, file := range p.files {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			selector, ok := literal.Type.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "StreamError" {
				return true
			}
			for _, element := range literal.Elts {
				if pair, ok := element.(*ast.KeyValueExpr); ok {
					if key, ok := pair.Key.(*ast.Ident); ok && key.Name == "Code" {
						if code, ok := stringLiteral(pair.Value); ok {
							position := p.fset.Position(pair.Pos())
							out[emittedCode{200, code}] = append(out[emittedCode{200, code}], filepath.Base(position.Filename)+":"+strconv.Itoa(position.Line))
						}
					}
				}
			}
			return true
		})
	}
	return out
}

// registrySection returns the (status, code) rows of one registry section. Rows
// look like "| 409 | `project_exists` | ... |"; the code cell may be null.
func registrySection(t *testing.T, heading string) map[emittedCode]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(conformanceRepoRoot, "contracts/agents-api/error-codes.md"))
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("^\\| *([0-9]{3}) *\\| *(`[a-z0-9_]+`|null) *\\|")
	rows := map[emittedCode]bool{}
	inside, found := false, false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## ") {
			inside = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == heading
			found = found || inside
			continue
		}
		if !inside {
			continue
		}
		if match := row.FindStringSubmatch(line); match != nil {
			status, _ := strconv.Atoi(match[1])
			code := strings.Trim(match[2], "`")
			if code == "null" {
				code = ""
			}
			if rows[emittedCode{status, code}] {
				t.Errorf("error-codes.md %q lists %d %s twice", heading, status, code)
			}
			rows[emittedCode{status, code}] = true
		}
	}
	if !found {
		t.Fatalf("error-codes.md has no section %q", heading)
	}
	return rows
}

func compareRegistry(t *testing.T, heading string, emitted map[emittedCode][]string) {
	t.Helper()
	listed := registrySection(t, heading)
	var undocumented, unused []string
	for pair, sites := range emitted {
		if !listed[pair] {
			slices.Sort(sites)
			undocumented = append(undocumented, pair.String()+" at "+strings.Join(sites, ", "))
		}
	}
	for pair := range listed {
		if _, ok := emitted[pair]; !ok {
			unused = append(unused, pair.String())
		}
	}
	sort.Strings(undocumented)
	sort.Strings(unused)
	for _, entry := range undocumented {
		t.Errorf("%s: emitted but not listed in error-codes.md: %s", heading, entry)
	}
	for _, entry := range unused {
		t.Errorf("%s: listed in error-codes.md but never emitted: %s", heading, entry)
	}
}

// callStatuses collects the http.StatusX argument at index of every call to
// name, which is a local function or variable, or "http.Error".
func callStatuses(t *testing.T, p sourcePackage, name string, index int) map[int]bool {
	out := map[int]bool{}
	for _, file := range p.files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) <= index {
				return true
			}
			called := ""
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				called = fun.Name
			case *ast.SelectorExpr:
				if pkg, ok := fun.X.(*ast.Ident); ok {
					called = pkg.Name + "." + fun.Sel.Name
				}
			}
			if called == name {
				if status, ok := statusConstant(t, call.Args[index]); ok {
					out[status] = true
				}
			}
			return true
		})
	}
	return out
}

// registryStatuses returns the statuses of every row in one registry section,
// including rows whose code is null.
func registryStatuses(t *testing.T, heading string) map[int]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(conformanceRepoRoot, "contracts/agents-api/error-codes.md"))
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`^\| *([0-9]{3}) *\|`)
	out := map[int]bool{}
	inside := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## ") {
			inside = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == heading
			continue
		}
		if match := row.FindStringSubmatch(line); inside && match != nil {
			status, _ := strconv.Atoi(match[1])
			out[status] = true
		}
	}
	return out
}

// TestUncodedResponsesMatchRegistry covers responses without a code: the
// console sign-in errors and the plain-text machine transport errors.
func TestUncodedResponsesMatchRegistry(t *testing.T) {
	console := parseSourcePackage(t, filepath.Join(conformanceRepoRoot, "services/core-console"))
	signIn := callStatuses(t, console, "authError", 1)
	if got, want := sortedStatuses(registryStatuses(t, "Console sign-in responses")), sortedStatuses(signIn); !slices.Equal(got, want) {
		t.Errorf("Console sign-in responses list %v; authError writes %v", got, want)
	}
	transport := map[int]bool{}
	enrollment := parseSourcePackage(t, filepath.Join(conformanceRepoRoot, "services/agents-api/internal/runtimeenrollment"))
	for status := range callStatuses(t, enrollment, "fail", 0) {
		transport[status] = true
	}
	node := parseSourcePackage(t, filepath.Join(conformanceRepoRoot, "services/agents-api/internal/sandbox/node"))
	for status := range callStatuses(t, node, "http.Error", 2) {
		transport[status] = true
	}
	if got, want := sortedStatuses(registryStatuses(t, "Plain-text transport responses")), sortedStatuses(transport); !slices.Equal(got, want) {
		t.Errorf("Plain-text transport responses list %v; the handlers write %v", got, want)
	}
}

// TestErrorCodeRegistryMatchesEmittedCodes keeps the error code registry and
// the service's (status, code) pairs equal in both directions (G3).
func TestErrorCodeRegistryMatchesEmittedCodes(t *testing.T) {
	collectTypedCodes(t)
	// A code assigned from a helper's (status, code) result is skipped at the
	// writer call, so returnedCodes must run on every package.
	api := parseSourcePackage(t, ".")
	codes := emittedCodes(t, api, map[string][2]int{
		"writeError": {1, 2}, "writeAPIError": {1, 2}, "writeCoreError": {1, 2},
	})
	returnedCodes(t, api, codes)
	compareRegistry(t, "HTTP API codes", codes)

	console := parseSourcePackage(t, filepath.Join(conformanceRepoRoot, "services/core-console"))
	consoleCodes := emittedCodes(t, console, map[string][2]int{"consoleCoreError": {1, 2}})
	returnedCodes(t, console, consoleCodes)
	compareRegistry(t, "Console codes", consoleCodes)

	// The stream writes its own interruption; the store records the error objects
	// of saved Session events.
	events := streamErrorCodes(api)
	store := parseSourcePackage(t, filepath.Join(conformanceRepoRoot, "services/agents-api/internal/store"))
	for code, sites := range streamErrorCodes(store) {
		events[code] = append(events[code], sites...)
	}
	compareRegistry(t, "Session event error codes", events)

	gateway := parseSourcePackage(t, filepath.Join(conformanceRepoRoot, "internal/agentdaemon/gateway"))
	daemon := emittedCodes(t, gateway, map[string][2]int{"writeAuthError": {1, 2}})
	returnedCodes(t, gateway, daemon)
	compareRegistry(t, "Runtime daemon transport codes", daemon)
}

// TestInstallationDomainCodesMatchRegistry keeps the relayed installer
// rejections equal to the codes deploy/install/ingress.py answers with before
// it accepts a domain request. verify and execute run after the 202, so their
// codes reach the caller only as the status message.
func TestInstallationDomainCodesMatchRegistry(t *testing.T) {
	const source = "deploy/install/ingress.py"
	raw, err := os.ReadFile(filepath.Join(conformanceRepoRoot, source))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	definition := regexp.MustCompile(`(?m)^def (\w+)\(`)
	enclosing := func(offset int) string {
		name := ""
		for _, match := range definition.FindAllStringSubmatchIndex(text[:offset], -1) {
			name = text[match[2]:match[3]]
		}
		return name
	}
	line := func(offset int) string { return source + ":" + strconv.Itoa(strings.Count(text[:offset], "\n")+1) }
	emitted := map[emittedCode][]string{}
	add := func(status int, code string, offset int) {
		key := emittedCode{status, code}
		emitted[key] = append(emitted[key], line(offset))
	}
	raised := regexp.MustCompile(`DomainError\("(\w+)", (?:"(?:[^"\\]|\\.)*"|[\w.()]+)(?:, (\d{3}))?\)`)
	for _, match := range raised.FindAllStringSubmatchIndex(text, -1) {
		if name := enclosing(match[0]); name == "verify" || name == "execute" {
			continue
		}
		status := 400
		if match[4] >= 0 {
			status, _ = strconv.Atoi(text[match[4]:match[5]])
		}
		add(status, text[match[2]:match[3]], match[0])
	}
	replied := regexp.MustCompile(`self\.reply\((\d{3}), \{"error": \{"code": "(\w+)"`)
	for _, match := range replied.FindAllStringSubmatchIndex(text, -1) {
		status, _ := strconv.Atoi(text[match[2]:match[3]])
		add(status, text[match[4]:match[5]], match[0])
	}
	// The handler's fallback maps an installer error and invalid JSON to fixed pairs.
	codes := regexp.MustCompile(`"(\w+)" if isinstance\(error, oac_cli\.OacError\) else "(\w+)"`).FindStringSubmatchIndex(text)
	statuses := regexp.MustCompile(`(\d{3}) if isinstance\(error, oac_cli\.OacError\) else (\d{3})`).FindStringSubmatch(text)
	if codes == nil || statuses == nil {
		t.Fatalf("%s: the request handler's fallback error mapping changed; update this test", source)
	}
	busy, _ := strconv.Atoi(statuses[1])
	invalid, _ := strconv.Atoi(statuses[2])
	add(busy, text[codes[2]:codes[3]], codes[0])
	add(invalid, text[codes[4]:codes[5]], codes[0])
	compareRegistry(t, "Installation domain setup codes", emitted)
}
