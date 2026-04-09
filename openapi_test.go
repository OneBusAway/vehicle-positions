package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// These tests enforce that openapi.yaml stays in lock-step with the HTTP
// routes and validation constants declared in main.go / handlers.go. They do
// not pull in a full OpenAPI library; a minimal structural view of the spec
// is enough to guard the invariants that catch drift.

const openAPIPath = "openapi.yaml"

type openAPISpec struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas         map[string]any `yaml:"schemas"`
		SecuritySchemes map[string]any `yaml:"securitySchemes"`
	} `yaml:"components"`
}

func loadOpenAPISpec(t *testing.T) *openAPISpec {
	t.Helper()
	data, err := os.ReadFile(openAPIPath)
	require.NoError(t, err, "openapi.yaml must exist at repo root")
	require.NotEmpty(t, data, "openapi.yaml must not be empty")

	var spec openAPISpec
	require.NoError(t, yaml.Unmarshal(data, &spec), "openapi.yaml must parse as valid YAML")
	return &spec
}

// muxRoutePattern captures (METHOD, PATH) from every mux registration in
// main.go. main.go consistently writes routes as:
//
//	mux.Handle("METHOD /path", ...)
//	mux.HandleFunc("METHOD /path", ...)
var muxRoutePattern = regexp.MustCompile(`mux\.(?:Handle|HandleFunc)\("([A-Z]+)\s+([^"]+)"`)

// muxRegistrationPattern matches every mux.Handle/HandleFunc call regardless of
// how the pattern argument is spelled (string literal, constant, expression).
// It is used to cross-check muxRoutePattern so a refactor that moves routes
// into constants (e.g. mux.Handle(routeFoo, ...)) fails loudly instead of
// silently hiding the route from the drift guard.
var muxRegistrationPattern = regexp.MustCompile(`mux\.(?:Handle|HandleFunc)\(`)

type registeredRoute struct{ method, path string }

func extractRoutesFromMainGo(t *testing.T) []registeredRoute {
	t.Helper()
	data, err := os.ReadFile("main.go")
	require.NoError(t, err, "main.go must exist at repo root")
	src := string(data)

	matches := muxRoutePattern.FindAllStringSubmatch(src, -1)
	require.NotEmpty(t, matches, "expected mux route registrations in main.go")

	// Fail loudly if any mux registration slipped past muxRoutePattern (e.g. a
	// route whose method+path argument isn't a literal string). Otherwise the
	// drift guard would silently skip it.
	totalRegistrations := len(muxRegistrationPattern.FindAllString(src, -1))
	require.Equal(t, totalRegistrations, len(matches),
		"muxRoutePattern missed %d mux registration(s) in main.go — a route argument is probably not a literal string",
		totalRegistrations-len(matches))

	routes := make([]registeredRoute, 0, len(matches))
	for _, m := range matches {
		routes = append(routes, registeredRoute{method: m[1], path: m[2]})
	}
	return routes
}

func TestOpenAPI_Version(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)
	assert.Equal(t, "3.1.0", spec.OpenAPI, "spec must target OpenAPI 3.1.0")
}

func TestOpenAPI_InfoComplete(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)
	assert.NotEmpty(t, spec.Info.Title, "info.title must be set")
	assert.NotEmpty(t, spec.Info.Version, "info.version must be set")
}

func TestOpenAPI_SecurityScheme(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)
	raw, ok := spec.Components.SecuritySchemes["bearerAuth"]
	require.True(t, ok, "bearerAuth security scheme must be defined")

	scheme, ok := raw.(map[string]any)
	require.True(t, ok, "bearerAuth must be a YAML mapping")
	assert.Equal(t, "http", scheme["type"])
	assert.Equal(t, "bearer", scheme["scheme"])
	assert.Equal(t, "JWT", scheme["bearerFormat"])
}

func TestOpenAPI_ErrorResponseSchema(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)
	raw, ok := spec.Components.Schemas["ErrorResponse"]
	require.True(t, ok, "ErrorResponse schema must exist (matches writeJSON error shape)")

	schema, ok := raw.(map[string]any)
	require.True(t, ok)

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "ErrorResponse must declare properties")
	_, ok = props["error"]
	assert.True(t, ok, "ErrorResponse.properties.error must exist")
}

// TestOpenAPI_AllRoutesDocumented is the critical drift guard: every route
// registered in main.go must have a matching path+method entry in the spec.
// Adding a new endpoint without documenting it will fail CI here.
func TestOpenAPI_AllRoutesDocumented(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)
	routes := extractRoutesFromMainGo(t)

	for _, r := range routes {
		pathItem, ok := spec.Paths[r.path]
		if !ok {
			t.Errorf("main.go registers %s %s but openapi.yaml has no entry for this path", r.method, r.path)
			continue
		}
		method := strings.ToLower(r.method)
		if _, ok := pathItem[method]; !ok {
			t.Errorf("main.go registers %s %s but openapi.yaml does not document this method", r.method, r.path)
		}
	}
}

// TestOpenAPI_NoExtraRoutes is the inverse drift guard: the spec must not
// document endpoints that no longer exist in main.go.
func TestOpenAPI_NoExtraRoutes(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)
	routes := extractRoutesFromMainGo(t)

	type key struct{ path, method string }
	registered := make(map[key]struct{}, len(routes))
	for _, r := range routes {
		registered[key{path: r.path, method: strings.ToLower(r.method)}] = struct{}{}
	}

	// Only these fields under a path item are operations — other keys like
	// "parameters", "summary", "description" must be ignored.
	operationMethods := map[string]struct{}{
		"get": {}, "post": {}, "put": {}, "delete": {},
		"patch": {}, "head": {}, "options": {}, "trace": {},
	}

	for path, pathItem := range spec.Paths {
		for field := range pathItem {
			if _, isOp := operationMethods[field]; !isOp {
				continue
			}
			if _, ok := registered[key{path: path, method: field}]; !ok {
				t.Errorf("openapi.yaml documents %s %s but main.go does not register this route", strings.ToUpper(field), path)
			}
		}
	}
}

// TestOpenAPI_LocationReportConstantsMatchCode cross-references the spec's
// LocationReport.vehicle_id validation against the constants in handlers.go,
// so changing the regex or length limit in Go forces a spec update.
func TestOpenAPI_LocationReportConstantsMatchCode(t *testing.T) {
	t.Parallel()
	spec := loadOpenAPISpec(t)

	schema, ok := spec.Components.Schemas["LocationReport"].(map[string]any)
	require.True(t, ok, "LocationReport schema must be a mapping")

	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "LocationReport.properties must exist")

	vehicleID, ok := props["vehicle_id"].(map[string]any)
	require.True(t, ok, "LocationReport.properties.vehicle_id must exist")

	assert.Equal(t, vehicleIDPattern.String(), vehicleID["pattern"],
		"LocationReport.vehicle_id.pattern must match vehicleIDPattern in handlers.go")

	// yaml.v3 decodes small integers into int when the target is interface{};
	// accept int64 too in case the platform differs.
	var maxLen int
	switch v := vehicleID["maxLength"].(type) {
	case int:
		maxLen = v
	case int64:
		maxLen = int(v)
	default:
		t.Fatalf("LocationReport.vehicle_id.maxLength must be an integer, got %T", vehicleID["maxLength"])
	}
	assert.Equal(t, maxVehicleIDLength, maxLen,
		"LocationReport.vehicle_id.maxLength must match maxVehicleIDLength in handlers.go")
}
