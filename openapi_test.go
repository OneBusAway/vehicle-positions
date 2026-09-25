package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

// schemaStructs pairs a response schema with the Go struct whose JSON encoding
// it describes. TestOpenAPI_SchemaPropertiesMatchStructs holds each pair to its
// struct's tags — the check that would have caught `User.active` when #92 added
// the field and this spec did not follow.
//
// Only response schemas belong here. Request schemas describe what a client may
// send, which is not always the same shape the server decodes into.
var schemaStructs = map[string]string{
	"User":                 "UserResponse",
	"Vehicle":              "VehicleResponse",
	"Trip":                 "TripResponse",
	"TripSummary":          "TripSummary",
	"Assignment":           "AssignmentResponse",
	"LiveVehicles":         "liveVehiclesResponse",
	"LiveVehicleEntry":     "liveVehicleEntry",
	"TripList":             "tripListResponse",
	"TripTrail":            "tripTrailResponse",
	"TripTrailPoint":       "tripTrailPoint",
	"LocationHistory":      "locationHistoryResponse",
	"LocationHistoryEntry": "locationEntry",

	// Rider mode (#94).
	"RiderRegisterResponse": "riderRegisterResponse",
	"StartRideResponse":     "startRideResponse",
	"RideDestination":       "rideDestination",
	"PositionsResponse":     "positionsResponse",
	"EndRideResponse":       "endRideResponse",
	"RideSummary":           "rideSummaryJSON",
	"TripCoverage":          "tripStatusResponse",
	"RiderStatus":           "riderStatusResponse",
	"RiderGTFSStatus":       "riderGTFSStatus",
	"RiderFeedStatus":       "riderFeedStatus",
	"RiderTierCounts":       "riderTierCounts",
	"RiderRideCounts":       "riderRideCounts",
	"AdminRides":            "adminRidesResponse",
	"AdminRide":             "adminRideEntry",

	// Feed API keys and the GTFS catalog.
	"APIKey":             "APIKey",
	"CatalogRoutes":      "catalogRoutesResponse",
	"CatalogRoute":       "catalogRoute",
	"CatalogRouteTrips":  "catalogRouteTripsResponse",
	"CatalogTripSummary": "catalogTripSummary",
	"CatalogTrip":        "catalogTripResponse",
	"CatalogTripRoute":   "catalogTripRoute",
	"CatalogShape":       "catalogShape",
	"CatalogStop":        "catalogStop",
	"CatalogThresholds":  "catalogThresholds",
}

// requestSchemaStructs pairs a request schema with the Go struct the handler
// decodes it into. Only the property set is compared, not `required`: a request
// schema's `required` reflects what the handler validates, which the struct
// cannot express — an optional field and a mandatory one look identical in Go.
//
// The property set is still worth pinning, because every one of these schemas
// sets additionalProperties: false to mirror DisallowUnknownFields. A field the
// server accepts but the spec omits makes a spec-following client unable to
// send it at all.
var requestSchemaStructs = map[string]string{
	"LocationReport":          "LocationReport",
	"UpsertVehicleRequest":    "upsertVehicleRequest",
	"StartTripRequest":        "StartTripRequest",
	"EndTripRequest":          "EndTripRequest",
	"CreateAssignmentRequest": "AssignmentRequest",
	"CreateAPIKeyRequest":     "createAPIKeyRequest",
	"RiderRegisterRequest":    "riderRegisterRequest",
	"StartRideRequest":        "startRideRequest",
	"PositionsRequest":        "positionsRequest",
	"PositionUpload":          "positionUpload",
	"EndRideRequest":          "endRideRequest",
	"CreateUserRequest":       "CreateUserRequest",
	"UpdateUserRequest":       "UpdateUserRequest",
	"LoginRequest":            "LoginRequest",
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
	rider        bool
	apiKey       bool
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
// moduleFiles parses every non-test Go file in this module. rootOnly restricts
// the result to the server package at the repository root, which matters when
// a name is only unique there — cmd/ridersim declares its own
// startRideResponse and positionsResponse for talking to this API.
func moduleFiles(t *testing.T, rootOnly bool) (*token.FileSet, []*ast.File) {
	t.Helper()

	fileSet := token.NewFileSet()
	var files []*ast.File

	walkErr := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rootOnly && path != "." {
				return filepath.SkipDir
			}
			return skipNonModuleDir(path, entry)
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		parsed, err := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		require.NoErrorf(t, err, "parsing %s", path)
		files = append(files, parsed)
		return nil
	})
	require.NoError(t, walkErr)
	require.NotEmpty(t, files, "expected Go sources in the module")

	return fileSet, files
}

func extractRegisteredRoutes(t *testing.T) []registeredRoute {
	t.Helper()

	routes := make([]registeredRoute, 0, len(htmlUIRoutes))

	fileSet, files := moduleFiles(t, false)
	scopes := resolveMiddleware(files)

	for function, scope := range scopes {
		ast.Inspect(function, func(node ast.Node) bool {
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
				auth:   wrappedBy(call.Args[1], scope, "requireAuth"),
				admin:  wrappedBy(call.Args[1], scope, "requireAdmin"),
				rider:  wrappedBy(call.Args[1], scope, "requireRider"),
				apiKey: wrappedBy(call.Args[1], scope, "requireAPIKey"),
			})
			return true
		})
	}

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

	// A route can be registered more than once behind different middleware —
	// newMux mounts the feed with requireAPIKey or bare depending on
	// FEED_AUTH_ENABLED. Merge those into one route that carries every
	// middleware any branch applies, so the spec is held to the guarded form.
	merged := make(map[string]registeredRoute, len(all))
	order := make([]string, 0, len(all))
	for _, route := range all {
		if _, isUI := htmlUIRoutes[route.String()]; isUI {
			continue
		}
		key := route.String()
		existing, seen := merged[key]
		if !seen {
			merged[key] = route
			order = append(order, key)
			continue
		}
		existing.auth = existing.auth || route.auth
		existing.admin = existing.admin || route.admin
		existing.rider = existing.rider || route.rider
		existing.apiKey = existing.apiKey || route.apiKey
		merged[key] = existing
	}

	api := make([]registeredRoute, 0, len(order))
	for _, key := range order {
		api = append(api, merged[key])
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

// middlewareConstructors are the functions that build an auth middleware. A
// route is classified by which of these produced the wrapper around its
// handler, not by what the local variable happens to be called.
var middlewareConstructors = map[string]struct{}{
	"requireAuth": {}, "requireAdmin": {}, "requireRider": {}, "requireAPIKey": {},
}

// middlewareScope maps the identifiers naming an auth middleware inside one
// function to the constructor that produced each.
type middlewareScope map[string]string

// resolveMiddleware works out, for every function in the module, which
// identifiers hold an auth middleware.
//
// An identifier acquires one two ways. By assignment — `authMiddleware :=
// requireAuth(jwtSecret)` in newMux, `auth := requireRider(s.jwtSecret)` in
// registerRiderRoutes. Or by parameter: newMux calls
// `registerGTFSRoutes(mux, authMiddleware, catalog)`, and the callee knows that
// same middleware as `auth`.
//
// Following both matters because the alternative is silently wrong in the
// dangerous direction: an unresolved identifier looks like no middleware at
// all, so an authenticated route would be documented as public.
func resolveMiddleware(files []*ast.File) map[*ast.FuncDecl]middlewareScope {
	scopes := make(map[*ast.FuncDecl]middlewareScope)
	byName := make(map[string]*ast.FuncDecl)

	for _, file := range files {
		for _, decl := range file.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			scopes[function] = make(middlewareScope)
			if function.Recv == nil {
				byName[function.Name.Name] = function
			}
		}
	}

	// Assignments first: a call site can only pass on a middleware its own
	// function already resolved.
	for function, scope := range scopes {
		ast.Inspect(function, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
				return true
			}
			name, ok := assign.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			if constructor, ok := constructorOf(assign.Rhs[0], nil); ok {
				scope[name.Name] = constructor
			}
			return true
		})
	}

	// Then parameters, resolved from each call site.
	for caller, callerScope := range scopes {
		ast.Inspect(caller, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			callee, ok := byName[name.Name]
			if !ok || callee.Type.Params == nil {
				return true
			}

			position := 0
			for _, field := range callee.Type.Params.List {
				for _, param := range field.Names {
					if position < len(call.Args) && isMiddlewareType(field.Type) {
						if constructor, ok := constructorOf(call.Args[position], callerScope); ok {
							scopes[callee][param.Name] = constructor
						}
					}
					position++
				}
			}
			return true
		})
	}

	return scopes
}

// constructorOf reports which middleware constructor an expression yields,
// either by calling one directly or by naming an identifier already resolved
// to one.
func constructorOf(expr ast.Expr, scope middlewareScope) (string, bool) {
	switch value := expr.(type) {
	case *ast.CallExpr:
		name, ok := value.Fun.(*ast.Ident)
		if !ok {
			return "", false
		}
		if _, isMiddleware := middlewareConstructors[name.Name]; isMiddleware {
			return name.Name, true
		}
	case *ast.Ident:
		if constructor, ok := scope[value.Name]; ok {
			return constructor, true
		}
	}
	return "", false
}

// isMiddlewareType reports whether a parameter is declared as
// func(http.Handler) http.Handler — the shape every wrapper in this codebase
// takes.
func isMiddlewareType(expr ast.Expr) bool {
	signature, ok := expr.(*ast.FuncType)
	if !ok || signature.Params == nil || len(signature.Params.List) != 1 {
		return false
	}
	if signature.Results == nil || len(signature.Results.List) != 1 {
		return false
	}
	return isHTTPHandler(signature.Params.List[0].Type) && isHTTPHandler(signature.Results.List[0].Type)
}

func isHTTPHandler(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Handler" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "http"
}

// wrappedBy reports whether the handler argument of a mux registration is
// wrapped, at any depth, in a middleware built by the named constructor —
// newMux composes them as authMiddleware(adminMiddleware(handler)).
func wrappedBy(handler ast.Expr, scope middlewareScope, constructor string) bool {
	wrapped := false
	ast.Inspect(handler, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		if name.Name == constructor || scope[name.Name] == constructor {
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

// declaresScheme reports whether an operation's security block requires the
// named scheme.
func declaresScheme(value any, scheme string) bool {
	requirements, ok := value.([]any)
	if !ok {
		return false
	}
	for _, requirement := range requirements {
		entry, ok := requirement.(map[string]any)
		if !ok {
			continue
		}
		if _, named := entry[scheme]; named {
			return true
		}
	}
	return false
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

		switch {
		case route.apiKey:
			// The feed is mounted behind requireAPIKey only when
			// FEED_AUTH_ENABLED is set, so the spec documents the guarded
			// form and says when it applies.
			assert.Truef(t, overridden && declaresScheme(security, "apiKey"),
				"%s wraps %s in requireAPIKey, so the spec must declare `security: [{apiKey: []}]`",
				route.source, route)
			assert.Containsf(t, operation["responses"], "401",
				"%s wraps %s in requireAPIKey, so the spec must document a 401 response", route.source, route)
			assert.Containsf(t, operation["responses"], "500",
				"%s wraps %s in requireAPIKey, which fails with 500 when the key lookup does, so the spec must document a 500 response",
				route.source, route)

		case route.rider:
			// The rider API carries its own token: requireRider accepts a JWT
			// with the rider role, not the driver/admin one the global scheme
			// describes, so these operations must name riderAuth explicitly.
			assert.Truef(t, overridden && declaresScheme(security, "riderAuth"),
				"%s wraps %s in requireRider, so the spec must declare `security: [{riderAuth: []}]`",
				route.source, route)
			assert.Containsf(t, operation["responses"], "401",
				"%s wraps %s in requireRider, so the spec must document a 401 response", route.source, route)
			assert.Containsf(t, operation["responses"], "403",
				"%s wraps %s in requireRider, which rejects a non-rider role, so the spec must document a 403 response",
				route.source, route)
			assert.Containsf(t, operation["responses"], "500",
				"%s wraps %s in requireRider, which fails with 500 when the revocation lookup does, so the spec must document a 500 response",
				route.source, route)

		case route.auth:
			assert.Falsef(t, overridden,
				"%s wraps %s in requireAuth, so the spec must inherit the global bearerAuth requirement rather than override security",
				route.source, route)
			assert.Containsf(t, operation["responses"], "401",
				"%s wraps %s in requireAuth, so the spec must document a 401 response", route.source, route)
			// requireAuth admits only the staff roles, so a valid token with
			// any other role — a rider token, handed out anonymously — is a
			// 403 rather than a 401.
			assert.Containsf(t, operation["responses"], "403",
				"%s wraps %s in requireAuth, which rejects a non-staff role, so the spec must document a 403 response",
				route.source, route)
			// Since #98 the token is checked against the revocation list, and a
			// failed lookup is a 500 — failing closed rather than letting a
			// possibly-revoked token through.
			assert.Containsf(t, operation["responses"], "500",
				"%s wraps %s in requireAuth, which fails with 500 when the revocation lookup does, so the spec must document a 500 response",
				route.source, route)

		default:
			assert.Truef(t, overridden && isEmptyList(security),
				"%s registers %s behind no auth middleware, so the spec must opt out with `security: []`",
				route.source, route)
		}
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
		listLimit     = "#/components/schemas/ListLimit"
		listOffset    = "#/components/schemas/ListOffset"
		riderLimit    = "#/components/schemas/RiderRideListLimit"
		riderBatch    = "#/components/schemas/PositionsRequest/properties/positions"
		riderRegister = "#/components/schemas/RiderRegisterResponse/properties"
		password      = "#/components/schemas/Password"
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
		{listLimit, "maximum", maxListLimit, "maxListLimit"},
		{listLimit, "default", defaultListLimit, "defaultListLimit"},
		{listOffset, "maximum", maxListOffset, "maxListOffset"},
		{riderLimit, "maximum", maxRiderRideListLimit, "maxRiderRideListLimit"},
		{riderLimit, "default", defaultRiderRideListLimit, "defaultRiderRideListLimit"},
		{riderBatch, "maxItems", riderMaxBatchSize, "riderMaxBatchSize"},
		{password, "minLength", minPasswordLength, "minPasswordLength"},
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

// structJSONField is one marshalled field of a Go struct.
type structJSONField struct {
	name      string
	omitEmpty bool
}

// structJSONFields returns, for every named struct in the module, the JSON
// fields it marshals to. Fields tagged `json:"-"` are skipped, and an untagged
// exported field marshals under its Go name.
func structJSONFields(t *testing.T) map[string][]structJSONField {
	t.Helper()

	structs := make(map[string][]structJSONField)
	_, files := moduleFiles(t, true)
	for _, parsed := range files {
		ast.Inspect(parsed, func(node ast.Node) bool {
			spec, ok := node.(*ast.TypeSpec)
			if !ok {
				return true
			}
			definition, ok := spec.Type.(*ast.StructType)
			if !ok {
				return true
			}

			fields := make([]structJSONField, 0, len(definition.Fields.List))
			for _, field := range definition.Fields.List {
				if len(field.Names) == 0 {
					continue // Embedded field; none of the mapped structs use one.
				}
				name, omitEmpty, marshalled := jsonTag(field)
				if !marshalled {
					continue
				}
				fields = append(fields, structJSONField{name: name, omitEmpty: omitEmpty})
			}
			structs[spec.Name.Name] = fields
			return true
		})
	}

	return structs
}

// jsonTag reads a struct field's encoding/json tag. It reports the wire name,
// whether the field is omitted when empty, and whether it is marshalled at all.
func jsonTag(field *ast.Field) (name string, omitEmpty, marshalled bool) {
	name = field.Names[0].Name
	if field.Tag == nil {
		return name, false, field.Names[0].IsExported()
	}

	value, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return name, false, field.Names[0].IsExported()
	}
	tag, ok := reflect.StructTag(value).Lookup("json")
	if !ok {
		return name, false, field.Names[0].IsExported()
	}

	parts := strings.Split(tag, ",")
	if parts[0] == "-" && len(parts) == 1 {
		return "", false, false
	}
	if parts[0] != "" {
		name = parts[0]
	}
	return name, slices.Contains(parts[1:], "omitempty"), true
}

// TestOpenAPI_SchemaPropertiesMatchStructs holds every response schema to the
// Go struct it describes: the property set must match the struct's JSON tags
// exactly, and a field marshalled unconditionally must be listed in `required`.
//
// This is the guard the earlier rounds lacked. Routes and constraints were
// pinned to Go, but schema bodies were not, so `User.active` could be added in
// #92 and go undocumented without anything failing.
func TestOpenAPI_SchemaPropertiesMatchStructs(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)
	mappings := spec.mappings()
	structs := structJSONFields(t)

	for schemaName, structName := range schemaStructs {
		t.Run(schemaName, func(t *testing.T) {
			schema, ok := mappings["#/components/schemas/"+schemaName]
			require.Truef(t, ok, "schema %s must exist", schemaName)

			fields, ok := structs[structName]
			require.Truef(t, ok, "Go struct %s must exist", structName)
			require.NotEmptyf(t, fields, "Go struct %s must marshal at least one field", structName)

			properties, ok := schema["properties"].(map[string]any)
			require.Truef(t, ok, "%s must declare properties", schemaName)

			required := make(map[string]struct{})
			if listed, ok := schema["required"].([]any); ok {
				for _, name := range listed {
					required[name.(string)] = struct{}{}
				}
			}

			documented := make([]string, 0, len(properties))
			for name := range properties {
				documented = append(documented, name)
			}
			marshalled := make([]string, 0, len(fields))
			for _, field := range fields {
				marshalled = append(marshalled, field.name)
			}
			slices.Sort(documented)
			slices.Sort(marshalled)
			assert.Equalf(t, marshalled, documented,
				"%s properties must match the JSON fields %s marshals", schemaName, structName)

			for _, field := range fields {
				_, isRequired := required[field.name]
				if field.omitEmpty {
					assert.Falsef(t, isRequired,
						"%s.%s is omitempty in %s, so it must not be listed as required",
						schemaName, field.name, structName)
					continue
				}
				assert.Truef(t, isRequired,
					"%s.%s is always marshalled by %s, so it must be listed as required",
					schemaName, field.name, structName)
			}
		})
	}
}

// TestOpenAPI_RequestSchemaPropertiesMatchStructs holds each request schema's
// property set to the struct the handler decodes into.
//
// Response schemas were already pinned; request bodies were the remaining gap,
// and it is the one that bites hardest: these schemas all set
// additionalProperties: false, so a field the spec omits is a field a
// spec-following client cannot send.
func TestOpenAPI_RequestSchemaPropertiesMatchStructs(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)
	mappings := spec.mappings()
	structs := structJSONFields(t)

	for schemaName, structName := range requestSchemaStructs {
		t.Run(schemaName, func(t *testing.T) {
			schema, ok := mappings["#/components/schemas/"+schemaName]
			require.Truef(t, ok, "schema %s must exist", schemaName)

			fields, ok := structs[structName]
			require.Truef(t, ok, "Go struct %s must exist", structName)
			require.NotEmptyf(t, fields, "Go struct %s must decode at least one field", structName)

			properties, ok := schema["properties"].(map[string]any)
			require.Truef(t, ok, "%s must declare properties", schemaName)

			documented := make([]string, 0, len(properties))
			for name := range properties {
				documented = append(documented, name)
			}
			decoded := make([]string, 0, len(fields))
			for _, field := range fields {
				decoded = append(decoded, field.name)
			}
			slices.Sort(documented)
			slices.Sort(decoded)

			assert.Equalf(t, decoded, documented,
				"%s properties must match the JSON fields %s decodes; it sets additionalProperties: false, so anything missing here is unsendable",
				schemaName, structName)
		})
	}
}

// TestOpenAPI_EveryRequestBodyIsPinned keeps requestSchemaStructs complete.
// The map is maintained by hand, and a request schema left out of it is simply
// never checked — which is how UpdateUserRequest.password went undocumented
// after the request-schema guard already existed. So every schema an operation
// takes as its JSON body must be mapped.
func TestOpenAPI_EveryRequestBodyIsPinned(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)

	bodies := 0
	for path, pathItem := range spec.Paths {
		for method, raw := range pathItem {
			if _, isOperation := operationMethods[method]; !isOperation {
				continue
			}
			operation, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			schema := jsonRequestSchema(operation)
			if schema == "" {
				continue
			}
			bodies++
			assert.Containsf(t, requestSchemaStructs, schema,
				"%s %s takes %s as its request body, so requestSchemaStructs must map it to the struct the handler decodes into",
				strings.ToUpper(method), path, schema)
		}
	}

	require.NotZero(t, bodies, "expected operations with JSON request bodies")
}

// jsonRequestSchema returns the component name an operation's JSON request
// body references, or "" when it has none.
func jsonRequestSchema(operation map[string]any) string {
	body, ok := operation["requestBody"].(map[string]any)
	if !ok {
		return ""
	}
	content, ok := body["content"].(map[string]any)
	if !ok {
		return ""
	}
	media, ok := content["application/json"].(map[string]any)
	if !ok {
		return ""
	}
	schema, ok := media["schema"].(map[string]any)
	if !ok {
		return ""
	}
	ref, _ := schema["$ref"].(string)
	return strings.TrimPrefix(ref, "#/components/schemas/")
}
