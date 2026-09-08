package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configDocGlobs are the files a variable may be documented in. The guard
// deliberately does not name one canonical file: the reference has lived in
// docs/ and in the README at different times, and #102 adds a second one, so
// pinning a path would make this test fail for a reason that has nothing to do
// with whether the variable is documented.
var configDocGlobs = []string{"README.md", "docs/*.md"}

// configDocVariablePattern matches the first cell of a reference table row: a
// single backticked SHOUTY_NAME and nothing else. Requiring the whole cell to
// be the name keeps prose that happens to mention a variable from counting as
// documentation of it.
var configDocVariablePattern = regexp.MustCompile("^`([A-Z][A-Z0-9_]*)`$")

// TestConfigDoc_AllVariablesDocumented and TestConfigDoc_NoStaleVariables are
// the two halves of the same guard: a variable added to the code without a
// table row fails, and so does a row for a variable nothing reads any more.
// One direction alone rots — a reference can be complete and wrong.
func TestConfigDoc_AllVariablesDocumented(t *testing.T) {
	inSource := envVarsReadInSource(t)
	documented := documentedVariables(t)

	for _, name := range sortedNames(inSource) {
		assert.Contains(t, documented, name,
			"%s is read by the server but has no reference-table row in %s", name, configDocGlobs)
	}
}

func TestConfigDoc_NoStaleVariables(t *testing.T) {
	inSource := envVarsReadInSource(t)
	documented := documentedVariables(t)

	for _, name := range sortedNames(documented) {
		assert.Contains(t, inSource, name,
			"%s has a reference-table row in %s but is not read anywhere in the module", name, documented[name])
	}
}

// TestConfigDoc_ParsesVariables keeps the two comparisons above from passing
// vacuously: either extractor silently returning nothing would make every
// Contains assertion above run zero times.
func TestConfigDoc_ParsesVariables(t *testing.T) {
	inSource := envVarsReadInSource(t)
	require.NotEmpty(t, inSource, "the AST walk found no environment variables; the extractor is broken")

	documented := documentedVariables(t)
	require.NotEmpty(t, documented, "no variables parsed out of %s; the table format may have changed", configDocGlobs)
}

// TestConfigDoc_ExcludesTestOnlyVariables pins the walk's exclusion of _test.go
// files. Variables that exist only to drive tests — WRITE_FIXTURE today — are
// not operator-facing configuration and must not be required in the reference.
// If the walk stopped skipping test files this set would be empty, because
// every test-file variable would also appear in the production set.
func TestConfigDoc_ExcludesTestOnlyVariables(t *testing.T) {
	inSource := envVarsReadInSource(t)
	inTests := envVarsReadInTests(t)

	testOnly := make(map[string]struct{})
	for name := range inTests {
		if _, production := inSource[name]; !production {
			testOnly[name] = struct{}{}
		}
	}
	require.NotEmpty(t, testOnly, "no test-only variables found; the walk may no longer be skipping _test.go files")

	documented := documentedVariables(t)
	for _, name := range sortedNames(testOnly) {
		assert.NotContains(t, documented, name,
			"%s is read only by tests and should not be in the operator reference", name)
	}
}

// envVarsReadInSource returns every environment variable name the module reads
// outside of tests.
func envVarsReadInSource(t *testing.T) map[string]struct{} {
	t.Helper()
	return envVarsInFiles(parseModule(t, false))
}

// envVarsReadInTests is envVarsReadInSource for _test.go files only.
func envVarsReadInTests(t *testing.T) map[string]struct{} {
	t.Helper()
	return envVarsInFiles(parseModule(t, true))
}

// parseModule parses every .go file in this module, either the test files or
// the production ones. Nested modules (maglev/ has its own go.mod) and testdata
// directories are skipped for the same reason the Go toolchain skips them:
// their contents are not part of this module's build.
func parseModule(t *testing.T, testFiles bool) []*ast.File {
	t.Helper()

	fset := token.NewFileSet()
	var files []*ast.File
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == "." {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" {
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") != testFiles {
			return nil
		}
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		files = append(files, parsed)
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, files, "no Go files parsed; the walk root is wrong")
	return files
}

// envVarsInFiles collects the string literal passed as the first argument to
// any function that reads the environment.
func envVarsInFiles(files []*ast.File) map[string]struct{} {
	readers := envReaderNames(files)
	names := make(map[string]struct{})
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !isEnvRead(call, readers) || len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if name, err := strconv.Unquote(lit.Value); err == nil {
				names[name] = struct{}{}
			}
			return true
		})
	}
	return names
}

// envReaderNames discovers the module's env helpers rather than listing them,
// so a helper added later is covered without touching this test. A function
// qualifies when it forwards its own first string parameter to something that
// already reads the environment, which resolves wrappers over wrappers —
// envPositiveDurationOrDefault reaches os.Getenv only through
// envDurationOrDefault — by iterating to a fixed point.
func envReaderNames(files []*ast.File) map[string]struct{} {
	readers := make(map[string]struct{})
	for {
		grew := false
		for _, file := range files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Body == nil {
					continue
				}
				if _, known := readers[fn.Name.Name]; known {
					continue
				}
				key := firstStringParamName(fn)
				if key == "" || !forwardsKey(fn.Body, key, readers) {
					continue
				}
				readers[fn.Name.Name] = struct{}{}
				grew = true
			}
		}
		if !grew {
			return readers
		}
	}
}

// firstStringParamName returns the name of the function's first parameter when
// that parameter is a plain string, and "" otherwise.
func firstStringParamName(fn *ast.FuncDecl) string {
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return ""
	}
	first := fn.Type.Params.List[0]
	if ident, ok := first.Type.(*ast.Ident); !ok || ident.Name != "string" {
		return ""
	}
	if len(first.Names) == 0 {
		return ""
	}
	return first.Names[0].Name
}

func forwardsKey(body *ast.BlockStmt, key string, readers map[string]struct{}) bool {
	forwards := false
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !isEnvRead(call, readers) || len(call.Args) == 0 {
			return true
		}
		if ident, ok := call.Args[0].(*ast.Ident); ok && ident.Name == key {
			forwards = true
			return false
		}
		return true
	})
	return forwards
}

// isEnvRead reports whether the call reads the environment, either directly
// through the os package or through one of the discovered helpers.
func isEnvRead(call *ast.CallExpr, readers map[string]struct{}) bool {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		pkg, ok := fn.X.(*ast.Ident)
		return ok && pkg.Name == "os" && (fn.Sel.Name == "Getenv" || fn.Sel.Name == "LookupEnv")
	case *ast.Ident:
		_, known := readers[fn.Name]
		return known
	}
	return false
}

// documentedVariables returns every variable named in a reference table across
// the operator-facing docs, mapped to the file it was found in so a failure can
// name the file to edit.
func documentedVariables(t *testing.T) map[string]string {
	t.Helper()

	documented := make(map[string]string)
	for _, path := range configDocFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "|") {
				continue
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			match := configDocVariablePattern.FindStringSubmatch(strings.TrimSpace(cells[0]))
			if match == nil {
				continue
			}
			if _, seen := documented[match[1]]; !seen {
				documented[match[1]] = path
			}
		}
	}
	return documented
}

// configDocFiles expands configDocGlobs, skipping docs/superpowers — those are
// design specs and implementation plans, not operator documentation, and a
// variable named in a table there is not documented for the person deploying
// this.
func configDocFiles(t *testing.T) []string {
	t.Helper()

	var paths []string
	for _, glob := range configDocGlobs {
		matches, err := filepath.Glob(glob)
		require.NoError(t, err)
		for _, path := range matches {
			if strings.HasPrefix(filepath.ToSlash(path), "docs/superpowers/") {
				continue
			}
			paths = append(paths, path)
		}
	}
	require.NotEmpty(t, paths, "no documentation files matched %s", configDocGlobs)
	sort.Strings(paths)
	return paths
}

// sortedNames keeps failure output stable across runs. It is generic over the
// map's value type so it serves both the presence sets and the
// variable-to-file map.
func sortedNames[V any](set map[string]V) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
