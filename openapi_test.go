package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// These tests keep openapi.yaml in lock-step with the server. Every assertion
// compares the spec against something derived from Go source: the routes
// registered on the mux, the middleware wrapping each handler, or the
// validation constants in handlers.go. Assertions that would only restate what
// the spec's author typed into the YAML are deliberately absent — they can
// only fail when someone edits openapi.yaml, which is not drift.
//
// Prose is out of reach of all of this. Descriptions still have to be read
// against the handlers by hand.

const openAPIPath = "openapi.yaml"

// vehicleIDSchemaRef is the one schema every vehicle-id-shaped field in the
// spec must reference. It doubles as a location, because mappings() files that
// schema under the same string — see TestOpenAPI_VehicleIDSchemaIsSingleSource.
const vehicleIDSchemaRef = "#/components/schemas/VehicleID"

// htmlUIRoutes are the server-rendered admin UI routes — the pages and form
// posts registered by registerAdminUI in admin_page_handlers.go, plus the
// static asset mount. They serve HTML and assets rather than the JSON API, so
// openapi.yaml does not describe them.
//
// They are excluded by name rather than by scanning main.go alone, so that
// adding a server-rendered route fails TestOpenAPI_AllRoutesDocumented until
// someone decides whether it belongs in the spec.
var htmlUIRoutes = map[string]struct{}{
	"GET /static/":                                       {},
	"GET /admin":                                         {},
	"GET /admin/{$}":                                     {},
	"GET /admin/login":                                   {},
	"POST /admin/login":                                  {},
	"POST /admin/logout":                                 {},
	"GET /admin/dashboard":                               {},
	"GET /admin/map":                                     {},
	"GET /admin/trips":                                   {},
	"GET /admin/vehicles":                                {},
	"GET /admin/vehicles/new":                            {},
	"POST /admin/vehicles":                               {},
	"GET /admin/vehicles/{id}/edit":                      {},
	"POST /admin/vehicles/{id}":                          {},
	"POST /admin/vehicles/{id}/activate":                 {},
	"POST /admin/vehicles/{id}/deactivate":               {},
	"GET /admin/users":                                   {},
	"GET /admin/users/new":                               {},
	"POST /admin/users":                                  {},
	"GET /admin/users/{id}/edit":                         {},
	"POST /admin/users/{id}":                             {},
	"POST /admin/users/{id}/activate":                    {},
	"POST /admin/users/{id}/deactivate":                  {},
	"POST /admin/users/{id}/vehicles":                    {},
	"POST /admin/users/{id}/vehicles/{vehicleID}/remove": {},
}

// operationMethods are the path-item fields that describe an operation. Every
// other key under a path ("parameters", "summary", …) must be ignored.
var operationMethods = map[string]struct{}{
	"get": {}, "put": {}, "post": {}, "delete": {},
	"options": {}, "head": {}, "patch": {}, "trace": {},
}

type openAPISpec struct {
	Paths map[string]map[string]any `yaml:"paths"`

	// document is the same file decoded without a schema. The typed view above
	// reads better for the route checks; the untyped one lets the $ref and
	// vehicle-id walks visit every node without knowing its shape.
	document map[string]any
}

func loadOpenAPISpec(t *testing.T) *openAPISpec {
	t.Helper()

	data, err := os.ReadFile(openAPIPath)
	require.NoError(t, err, "openapi.yaml must exist at the repo root")

	var spec openAPISpec
	require.NoError(t, yaml.Unmarshal(data, &spec), "openapi.yaml must parse as valid YAML")
	require.NoError(t, yaml.Unmarshal(data, &spec.document))
	require.NotEmpty(t, spec.Paths, "openapi.yaml must document at least one path")
	return &spec
}

func (s *openAPISpec) operation(path, method string) (map[string]any, bool) {
	pathItem, ok := s.Paths[path]
	if !ok {
		return nil, false
	}
	operation, ok := pathItem[strings.ToLower(method)].(map[string]any)
	return operation, ok
}

// mappings returns every mapping in the document keyed by its location, using
// the same "#/components/schemas/Name" syntax that $ref uses. That shared
// spelling is what lets TestOpenAPI_AllRefsResolve look a reference up
// directly.
func (s *openAPISpec) mappings() map[string]map[string]any {
	found := make(map[string]map[string]any)

	var walk func(location string, node any)
	walk = func(location string, node any) {
		switch typed := node.(type) {
		case map[string]any:
			found[location] = typed
			for key, child := range typed {
				walk(location+"/"+key, child)
			}
		case []any:
			for i, child := range typed {
				walk(location+"/"+strconv.Itoa(i), child)
			}
		}
	}
	walk("#", s.document)

	return found
}

// registeredRoute is one mux registration found in the server's Go source,
// together with the middleware wrapping its handler.
type registeredRoute struct {
	method, path string
	source       string
	auth         bool
	admin        bool
}

func (r registeredRoute) String() string { return r.method + " " + r.path }

// extractRegisteredRoutes parses every non-test Go file in this module and
// returns the routes they register.
//
// It walks the AST rather than grepping the source, so a `mux.Handle(` inside a
// comment or an unrelated string cannot invent a phantom route, and a
// registration whose pattern is not a literal string is reported loudly instead
// of slipping past unseen. Walking the whole module rather than just main.go
// means routes registered elsewhere — registerAdminUI in
// admin_page_handlers.go, today — are visible too, and a future move into a
// subpackage would not blind the guard.
func extractRegisteredRoutes(t *testing.T) []registeredRoute {
	t.Helper()

	fileSet := token.NewFileSet()
	routes := make([]registeredRoute, 0, len(htmlUIRoutes))

	walkErr := filepath.WalkDir(".", func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return skipNonModuleDir(file, entry)
		}
		if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
			return nil
		}

		parsed, err := parser.ParseFile(fileSet, file, nil, parser.SkipObjectResolution)
		require.NoErrorf(t, err, "parsing %s", file)

		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			registrar, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (registrar.Sel.Name != "Handle" && registrar.Sel.Name != "HandleFunc") {
				return true
			}

			source := fileSet.Position(call.Pos()).String()
			pattern, ok := stringLiteral(call.Args[0])
			require.Truef(t, ok,
				"%s: mux route pattern is not a literal string, so the drift guard cannot read the route", source)

			method, path, ok := strings.Cut(pattern, " ")
			require.Truef(t, ok, "%s: route pattern %q has no HTTP method prefix", source, pattern)

			routes = append(routes, registeredRoute{
				method: method,
				path:   path,
				source: source,
				auth:   wrappedBy(call.Args[1], "authMiddleware"),
				admin:  wrappedBy(call.Args[1], "adminMiddleware"),
			})
			return true
		})
		return nil
	})
	require.NoError(t, walkErr)

	require.NotEmpty(t, routes, "expected mux route registrations in the server source")
	return routes
}

// skipNonModuleDir keeps the walk inside this module. It skips hidden
// directories, testdata, and — the one that matters — any directory carrying
// its own go.mod: a nested module is a separate build whose routes are not
// ours to document, and a developer checkout sitting in the tree should not be
// able to fail this suite.
func skipNonModuleDir(dir string, entry fs.DirEntry) error {
	if dir == "." {
		return nil
	}
	if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "testdata" {
		return filepath.SkipDir
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return filepath.SkipDir
	}
	return nil
}

// apiRoutes returns the registered routes openapi.yaml is expected to document,
// i.e. everything but the server-rendered admin UI.
func apiRoutes(t *testing.T) []registeredRoute {
	t.Helper()

	all := extractRegisteredRoutes(t)
	api := make([]registeredRoute, 0, len(all))
	for _, route := range all {
		if _, isUI := htmlUIRoutes[route.String()]; !isUI {
			api = append(api, route)
		}
	}

	require.NotEmpty(t, api, "expected JSON API routes outside the admin UI")
	return api
}

func stringLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// wrappedBy reports whether the handler argument of a mux registration is
// wrapped in a call to the named middleware at any depth — newMux composes them
// as authMiddleware(adminMiddleware(handler)).
func wrappedBy(handler ast.Expr, middleware string) bool {
	wrapped := false
	ast.Inspect(handler, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, ok := call.Fun.(*ast.Ident); ok && name.Name == middleware {
			wrapped = true
			return false
		}
		return true
	})
	return wrapped
}

func isEmptyList(value any) bool {
	list, ok := value.([]any)
	return ok && len(list) == 0
}

// TestOpenAPI_AllRoutesDocumented is the primary drift guard: every route the
// server registers must have a matching path and method in the spec. Adding an
// endpoint without documenting it fails here.
func TestOpenAPI_AllRoutesDocumented(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)

	for _, route := range apiRoutes(t) {
		if _, ok := spec.Paths[route.path]; !ok {
			t.Errorf("%s registers %s but openapi.yaml has no entry for this path", route.source, route)
			continue
		}
		if _, ok := spec.operation(route.path, route.method); !ok {
			t.Errorf("%s registers %s but openapi.yaml does not document this method", route.source, route)
		}
	}
}

// TestOpenAPI_NoExtraRoutes is the inverse guard: the spec must not describe
// endpoints the server no longer serves.
func TestOpenAPI_NoExtraRoutes(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)

	registered := make(map[string]struct{})
	for _, route := range apiRoutes(t) {
		registered[strings.ToLower(route.method)+" "+route.path] = struct{}{}
	}

	for path, pathItem := range spec.Paths {
		for field := range pathItem {
			if _, isOperation := operationMethods[field]; !isOperation {
				continue
			}
			if _, ok := registered[field+" "+path]; !ok {
				t.Errorf("openapi.yaml documents %s %s but no Go source registers this route",
					strings.ToUpper(field), path)
			}
		}
	}
}

// TestOpenAPI_AuthRequirementsMatchCode ties each operation's security block to
// the middleware actually wrapping its handler, so moving a route behind
// requireAuth — or out from behind it — without touching the spec fails here.
func TestOpenAPI_AuthRequirementsMatchCode(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)

	for _, route := range apiRoutes(t) {
		operation, ok := spec.operation(route.path, route.method)
		if !ok {
			continue // Reported by TestOpenAPI_AllRoutesDocumented.
		}

		security, overridden := operation["security"]
		if !route.auth {
			assert.Truef(t, overridden && isEmptyList(security),
				"%s registers %s without authMiddleware, so the spec must opt out with `security: []`",
				route.source, route)
			continue
		}

		assert.Falsef(t, overridden,
			"%s wraps %s in authMiddleware, so the spec must inherit the global bearerAuth requirement rather than override security",
			route.source, route)
		assert.Containsf(t, operation["responses"], "401",
			"%s wraps %s in authMiddleware, so the spec must document a 401 response", route.source, route)
	}
}

// TestOpenAPI_AdminRoutesDocumentForbidden asserts that every route behind
// requireAdmin documents the 403 it can return. This is the check that would
// have caught the spec's stale "the admin-role check is missing from the
// handler" notes: the moment adminMiddleware was applied to the user routes,
// the spec owed a 403.
//
// Only the positive direction is asserted. A handler may return 403 for its own
// reasons — POST /api/v1/trips/start does, when the driver is not assigned to
// the vehicle — so documenting 403 without adminMiddleware is not an error.
func TestOpenAPI_AdminRoutesDocumentForbidden(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)

	for _, route := range apiRoutes(t) {
		if !route.admin {
			continue
		}
		operation, ok := spec.operation(route.path, route.method)
		if !ok {
			continue // Reported by TestOpenAPI_AllRoutesDocumented.
		}
		assert.Containsf(t, operation["responses"], "403",
			"%s wraps %s in adminMiddleware, so the spec must document a 403 response", route.source, route)
	}
}

// TestOpenAPI_ConstraintsMatchCode pins the spec's validation keywords to the
// Go constants they mirror, so changing a limit in Go forces a spec update.
//
// Only constraints backed by a named constant are listed. The rest — the
// latitude and longitude bounds, the 100-character trip and route ids, the
// 8-character password minimum — are bare literals in the handlers, so
// checking them here would compare a literal against a literal and prove
// nothing. They stay a manual read.
func TestOpenAPI_ConstraintsMatchCode(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)
	mappings := spec.mappings()

	const (
		upsertVehicle = "#/components/schemas/UpsertVehicleRequest/properties"
		historyLimit  = "#/components/schemas/HistoryLimit"
		tripListLimit = "#/components/schemas/TripListLimit"
	)

	constraints := []struct {
		location string
		keyword  string
		want     any
		constant string
	}{
		{vehicleIDSchemaRef, "pattern", vehicleIDPattern.String(), "vehicleIDPattern"},
		{vehicleIDSchemaRef, "maxLength", maxVehicleIDLength, "maxVehicleIDLength"},
		{upsertVehicle + "/label", "maxLength", maxFieldLength, "maxFieldLength"},
		{upsertVehicle + "/agency_tag", "maxLength", maxFieldLength, "maxFieldLength"},
		{historyLimit, "maximum", maxHistoryLimit, "maxHistoryLimit"},
		{historyLimit, "default", defaultHistoryLimit, "defaultHistoryLimit"},
		{tripListLimit, "maximum", maxTripListLimit, "maxTripListLimit"},
		{tripListLimit, "default", defaultTripListLimit, "defaultTripListLimit"},
	}

	for _, constraint := range constraints {
		t.Run(constraint.location+"/"+constraint.keyword, func(t *testing.T) {
			node, ok := mappings[constraint.location]
			require.Truef(t, ok, "%s must exist in the spec", constraint.location)
			assert.Equalf(t, constraint.want, node[constraint.keyword],
				"%s %s must match %s in Go", constraint.location, constraint.keyword, constraint.constant)
		})
	}
}

// TestOpenAPI_VehicleIDSchemaIsSingleSource fails if any node other than the
// VehicleID schema itself carries the vehicle-id pattern. Without it a new
// endpoint could inline the constraints again and drift independently of
// handlers.go, which is the whole reason the shared schema exists — the check
// above only looks at the locations it is told about.
func TestOpenAPI_VehicleIDSchemaIsSingleSource(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)

	pattern := vehicleIDPattern.String()
	for location, node := range spec.mappings() {
		if location == vehicleIDSchemaRef {
			continue
		}
		assert.NotEqualf(t, pattern, node["pattern"],
			"%s inlines the vehicle-id pattern; reference %s instead so the constraints live in one place",
			location, vehicleIDSchemaRef)
	}
}

// TestOpenAPI_AllRefsResolve follows every $ref in the document. The spec leans
// hard on shared components, and a mistyped pointer is still valid YAML.
func TestOpenAPI_AllRefsResolve(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)

	mappings := spec.mappings()
	references := 0
	for location, node := range mappings {
		reference, ok := node["$ref"].(string)
		if !ok {
			continue
		}
		references++
		_, resolved := mappings[reference]
		assert.Truef(t, resolved, "%s references %s, which does not exist", location, reference)
	}

	require.NotZero(t, references, "expected the spec to use $ref")
}

// TestOpenAPI_HTMLUIExclusionsAreCurrent keeps the exclusion list honest in the
// direction the other tests cannot see. A newly added server-rendered route
// that is missing from the list already fails TestOpenAPI_AllRoutesDocumented;
// an entry left behind by a deleted route would silently widen the blind spot,
// so it fails here instead.
func TestOpenAPI_HTMLUIExclusionsAreCurrent(t *testing.T) {
	t.Parallel()

	registered := make(map[string]struct{})
	for _, route := range extractRegisteredRoutes(t) {
		registered[route.String()] = struct{}{}
	}

	for route := range htmlUIRoutes {
		assert.Containsf(t, registered, route,
			"htmlUIRoutes withholds %q from the spec, but no Go source registers it any more", route)
	}
}
