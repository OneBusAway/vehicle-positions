package main

import (
	"bufio"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// testMetricsToken is METRICS_TOKEN for the tests that switch /metrics on.
const testMetricsToken = "metrics-token-0123456789abcdef0123456789"

// metricsMux is the real mux with /metrics on and what it reads: the counts,
// the schedule (nil for none) and the alerts.
func metricsMux(t *testing.T, health *feedHealth, catalog *gtfsCatalog, alerts *alertWatcher) *http.ServeMux {
	t.Helper()
	return metricsMuxWithToken(t, testMetricsToken, health, catalog, alerts)
}

// metricsMuxWithToken is metricsMux with the token main would pass, "" for
// /metrics off.
func metricsMuxWithToken(t *testing.T, token string, health *feedHealth, catalog *gtfsCatalog, alerts *alertWatcher) *http.ServeMux {
	t.Helper()
	return newMux(&noopStore{}, nil, nil, testSecret, testTTLs, time.Time{}, nil, nil, false, false, token, nil, catalog, nil, alerts, nil, health)
}

// quietAlerts is an alert watcher with no trips, for tests that only need
// /metrics mounted.
func quietAlerts() *alertWatcher {
	return newAlertWatcher(&fakeTripLister{}, testAlertSettings)
}

func getMetrics(h http.Handler, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// scrape fetches /metrics with the token and parses it, failing the test on
// anything Prometheus would not accept.
func scrape(t *testing.T, h http.Handler) parsedMetrics {
	t.Helper()
	w := getMetrics(h, "Bearer "+testMetricsToken)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "text/plain; version=0.0.4; charset=utf-8", w.Header().Get("Content-Type"))
	return parseMetrics(t, w.Body.String())
}

// parsedMetrics is a scrape: each family's TYPE, and each sample's value keyed
// by the series as written, name and labels.
type parsedMetrics struct {
	types   map[string]string
	samples map[string]string
	body    string
}

// value is the sample for series, which must be there.
func (m parsedMetrics) value(t *testing.T, series string) string {
	t.Helper()
	v, ok := m.samples[series]
	require.Truef(t, ok, "no sample %s in\n%s", series, m.body)
	return v
}

// parseMetrics reads the text exposition format strictly: every family has
// HELP then TYPE before its samples, every sample belongs to the family above
// it (a counter's samples end in _total), and every value is a number.
func parseMetrics(t *testing.T, body string) parsedMetrics {
	t.Helper()
	m := parsedMetrics{types: make(map[string]string), samples: make(map[string]string), body: body}
	require.True(t, strings.HasSuffix(body, "\n"), "the exposition must end with a newline")
	family, helped := "", ""
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "# HELP "):
			name, help, ok := strings.Cut(strings.TrimPrefix(line, "# HELP "), " ")
			require.Truef(t, ok && help != "", "HELP without text: %q", line)
			helped = name
		case strings.HasPrefix(line, "# TYPE "):
			name, kind, ok := strings.Cut(strings.TrimPrefix(line, "# TYPE "), " ")
			require.Truef(t, ok, "bad TYPE line %q", line)
			require.Equalf(t, helped, name, "TYPE %s does not follow its HELP", name)
			require.Containsf(t, []string{"counter", "gauge"}, kind, "unexpected type in %q", line)
			_, seen := m.types[name]
			require.Falsef(t, seen, "family %s declared twice", name)
			m.types[name] = kind
			family = name
		default:
			series, value, ok := strings.Cut(line, " ")
			require.Truef(t, ok, "bad sample line %q", line)
			name, _, _ := strings.Cut(series, "{")
			require.Equalf(t, family, name, "sample %q is not under its family's TYPE", line)
			if m.types[family] == "counter" {
				require.Truef(t, strings.HasSuffix(name, "_total"), "counter %s must end in _total", name)
			}
			_, err := strconv.ParseFloat(value, 64)
			require.NoErrorf(t, err, "sample %q has no numeric value", line)
			_, dup := m.samples[series]
			require.Falsef(t, dup, "series %s written twice", series)
			m.samples[series] = value
		}
	}
	require.NoError(t, scanner.Err())
	require.NotEmpty(t, m.samples, "the scrape held no samples")
	return m
}

// fixtureCatalog is the test schedule, loaded from source so a test can
// check the source never reaches the output.
func fixtureCatalog(t *testing.T, source string, loadedAt time.Time) (*gtfsCatalog, *rider.Index) {
	t.Helper()
	body, err := os.ReadFile("rider/testdata/fixture.zip")
	require.NoError(t, err)
	ix, err := rider.IndexFromBytes(body, source, loadedAt)
	require.NoError(t, err)
	return newGTFSCatalog(func() *rider.Index { return ix }, rider.DefaultThresholds()), ix
}

func TestMetricsTokenFromEnv(t *testing.T) {
	tests := []struct {
		name      string
		enabled   string
		token     string
		wantToken string
		wantErr   string
	}{
		{name: "unset is off"},
		{name: "false is off, whatever the token", enabled: "false", token: testMetricsToken},
		{name: "a token alone does not turn it on", token: testMetricsToken},
		{name: "not a boolean", enabled: "yes", token: testMetricsToken, wantErr: "METRICS_ENABLED must be true or false"},
		{name: "on without a token", enabled: "true", wantErr: "METRICS_TOKEN must be set"},
		{name: "on with a short token", enabled: "true", token: strings.Repeat("a", metricsTokenMinLength-1), wantErr: "at least 32 characters"},
		{name: "on with a token of exactly the minimum", enabled: "true", token: strings.Repeat("a", metricsTokenMinLength), wantToken: strings.Repeat("a", metricsTokenMinLength)},
		{name: "on", enabled: "1", token: testMetricsToken, wantToken: testMetricsToken},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("METRICS_ENABLED", tc.enabled)
			t.Setenv("METRICS_TOKEN", tc.token)

			token, err := metricsTokenFromEnv()

			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				assert.Empty(t, token)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantToken, token)
		})
	}
}

// A setting that cannot be used leaves /metrics off, the direction that
// exposes nothing, and says why in the log.
func TestMetricsSettingFromEnv(t *testing.T) {
	tests := []struct {
		name, enabled, token string
		wantToken            string
		wantLog              string
	}{
		{name: "unset is off and quiet"},
		{name: "not a boolean", enabled: "yes", token: testMetricsToken, wantLog: "level=ERROR msg=\"metrics disabled: invalid configuration\""},
		{name: "on without a token", enabled: "true", wantLog: "level=ERROR msg=\"metrics disabled: invalid configuration\""},
		{name: "on with a short token", enabled: "true", token: "short", wantLog: "level=ERROR msg=\"metrics disabled: invalid configuration\""},
		{name: "on", enabled: "true", token: testMetricsToken, wantToken: testMetricsToken, wantLog: "level=INFO msg=\"metrics enabled at /metrics; scrapers must send the METRICS_TOKEN bearer token\""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			t.Setenv("METRICS_ENABLED", tc.enabled)
			t.Setenv("METRICS_TOKEN", tc.token)

			assert.Equal(t, tc.wantToken, metricsSettingFromEnv())

			if tc.wantLog == "" {
				assert.Empty(t, logs.String())
			} else {
				assert.Contains(t, logs.String(), tc.wantLog)
			}
			if tc.token != "" {
				assert.NotContains(t, logs.String(), tc.token, "the token must not be logged")
			}
		})
	}
}

// main parses the settings and hands newMux the result; newMux goes by what
// it is given and never looks at the environment, so a test or a caller
// cannot turn /metrics on or off behind main's back.
func TestMetricsRoute_GoesByTheTokenPassedNotTheEnvironment(t *testing.T) {
	t.Run("on in the environment, no token passed", func(t *testing.T) {
		t.Setenv("METRICS_ENABLED", "true")
		t.Setenv("METRICS_TOKEN", testMetricsToken)
		mux := metricsMuxWithToken(t, "", newFeedHealth(time.Now), nil, quietAlerts())

		w := getMetrics(mux, "Bearer "+testMetricsToken)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("off in the environment, a token passed", func(t *testing.T) {
		t.Setenv("METRICS_ENABLED", "")
		t.Setenv("METRICS_TOKEN", "")
		mux := metricsMuxWithToken(t, testMetricsToken, newFeedHealth(time.Now), nil, quietAlerts())

		m := scrape(t, mux)

		assert.Equal(t, "1", m.value(t, "vehicle_positions_alerts_listed"))
	})
}

// TestMain_PassesTheMetricsSetting pins the wiring main() cannot be run to
// show: it reads the setting with metricsSettingFromEnv and hands that value
// to newHandler as its metricsToken argument.
func TestMain_PassesTheMetricsSetting(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	require.NoError(t, err)

	funcs := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			funcs[fn.Name.Name] = fn
		}
	}
	require.Contains(t, funcs, "main")
	require.Contains(t, funcs, "newHandler")

	position, index := -1, 0
	for _, field := range funcs["newHandler"].Type.Params.List {
		for _, name := range field.Names {
			if name.Name == "metricsToken" {
				position = index
			}
			index++
		}
	}
	require.GreaterOrEqual(t, position, 0, "newHandler has no metricsToken parameter")

	parsedInto := map[string]bool{}
	var passed []string
	ast.Inspect(funcs["main"].Body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.AssignStmt:
			if len(n.Rhs) == 1 {
				if call, ok := n.Rhs[0].(*ast.CallExpr); ok {
					if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "metricsSettingFromEnv" {
						for _, lhs := range n.Lhs {
							if id, ok := lhs.(*ast.Ident); ok {
								parsedInto[id.Name] = true
							}
						}
					}
				}
			}
		case *ast.CallExpr:
			if fn, ok := n.Fun.(*ast.Ident); ok && fn.Name == "newHandler" {
				require.Greater(t, len(n.Args), position)
				if id, ok := n.Args[position].(*ast.Ident); ok {
					passed = append(passed, id.Name)
				} else {
					passed = append(passed, "<not a variable>")
				}
			}
		}
		return true
	})
	require.Len(t, passed, 1, "main should call newHandler once")
	assert.True(t, parsedInto[passed[0]], "main passes %s as newHandler's metricsToken, not the value metricsSettingFromEnv returned", passed[0])
}

func TestMetricsRoute_RequiresTheToken(t *testing.T) {
	adminToken, err := generateJWT(&User{ID: 2, Email: "admin@test.com", Role: "admin"}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)

	refused := []struct {
		name          string
		authorization string
		logged        string
	}{
		{"no header", "", "no bearer token"},
		{"wrong token", "Bearer " + strings.Repeat("x", len(testMetricsToken)), "wrong token"},
		{"the token with one character more", "Bearer " + testMetricsToken + "x", "wrong token"},
		{"the token without its last character", "Bearer " + testMetricsToken[:len(testMetricsToken)-1], "wrong token"},
		{"not a bearer token", "Basic " + testMetricsToken, "no bearer token"},
		{"an admin's token is not the metrics token", "Bearer " + adminToken, "wrong token"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			// A mux of its own, so this refusal is the first one and logged.
			mux := metricsMux(t, newFeedHealth(time.Now), nil, quietAlerts())

			w := getMetrics(mux, tc.authorization)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Equal(t, `Bearer realm="metrics"`, w.Header().Get("WWW-Authenticate"))
			assert.Equal(t, "a valid metrics token is required", decodeError(t, w))
			assert.NotContains(t, w.Body.String(), "vehicle_positions_")
			assert.Contains(t, logs.String(), "level=WARN msg=\"metrics request refused\" reason=\""+tc.logged+"\" ip=192.0.2.1 suppressed=0")
			assert.NotContains(t, logs.String(), testMetricsToken)
		})
	}

	t.Run("the token", func(t *testing.T) {
		m := scrape(t, metricsMux(t, newFeedHealth(time.Now), nil, quietAlerts()))
		assert.Equal(t, "1", m.value(t, "vehicle_positions_alerts_listed"))
	})
}

func TestMetrics_Output(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	health := newFeedHealth(clock.now)
	statuses := []struct {
		endpoint healthEndpoint
		status   int
		times    int
	}{
		{healthReports, http.StatusCreated, 9},
		{healthReports, http.StatusBadRequest, 1},
		{healthReports, http.StatusUnauthorized, 2},
		{healthReports, http.StatusTooManyRequests, 3},
		{healthReports, http.StatusInternalServerError, 4},
		{healthFeed, http.StatusOK, 8},
		{healthFeed, http.StatusUnauthorized, 5},
		{healthFeed, http.StatusBadRequest, 6},
		{healthFeed, http.StatusServiceUnavailable, 7},
	}
	for _, s := range statuses {
		for range s.times {
			health.record(s.endpoint, s.status)
		}
	}
	for _, vehicle := range []string{"bus-1", "bus-2"} {
		sendReport(t, health, liveReportPath, vehicle, http.StatusCreated)
	}
	// An hour and more later the first minute's counts have left the hour but
	// stay in the totals, so the two families must differ.
	clock.advance(healthWindowMinutes * time.Minute)
	health.record(healthReports, http.StatusCreated)
	health.record(healthFeed, http.StatusUnauthorized)
	sendReport(t, health, liveReportPath, "bus-3", http.StatusCreated)
	clock.advance(time.Minute)

	loadedAt := time.Date(2026, 11, 14, 4, 0, 0, 0, time.UTC)
	catalog, ix := fixtureCatalog(t, "rider/testdata/fixture.zip", loadedAt)

	now := time.Now()
	trips := &fakeTripLister{}
	alerts := newAlertWatcher(trips, testAlertSettings)
	alerts.watchingSince = now.Add(-time.Hour)
	alerts.record("bus-quiet", alertStop, now.Add(-5*time.Minute))
	alerts.record("bus-quiet-2", alertStop, now.Add(-6*time.Minute))
	reportEvery(alerts, "bus-still", alertStop, now.Add(-12*time.Minute), now, 5*time.Second)
	quiet, quiet2, still := alertTrip("bus-quiet", now.Add(-time.Hour)), alertTrip("bus-quiet-2", now.Add(-time.Hour)), alertTrip("bus-still", now.Add(-time.Hour))
	quiet2.ID, still.ID = 8, 9
	trips.trips = []TripSummary{quiet, quiet2, still}

	m := scrape(t, metricsMux(t, health, catalog, alerts))

	wantTypes := map[string]string{
		"vehicle_positions_reports_total":                 "counter",
		"vehicle_positions_reports_last_hour":             "gauge",
		"vehicle_positions_feed_fetches_total":            "counter",
		"vehicle_positions_feed_fetches_last_hour":        "gauge",
		"vehicle_positions_vehicles_reporting":            "gauge",
		"vehicle_positions_gtfs_loaded_timestamp_seconds": "gauge",
		"vehicle_positions_gtfs_routes":                   "gauge",
		"vehicle_positions_gtfs_trips":                    "gauge",
		"vehicle_positions_alerts":                        "gauge",
		"vehicle_positions_alerts_listed":                 "gauge",
		"vehicle_positions_build_info":                    "gauge",
	}
	assert.Equal(t, wantTypes, m.types)

	wantSamples := map[string]string{
		`vehicle_positions_reports_total{outcome="accepted"}`:              "13",
		`vehicle_positions_reports_total{outcome="invalid"}`:               "1",
		`vehicle_positions_reports_total{outcome="unauthorized"}`:          "2",
		`vehicle_positions_reports_total{outcome="rate_limited"}`:          "3",
		`vehicle_positions_reports_total{outcome="server_error"}`:          "4",
		`vehicle_positions_reports_last_hour{outcome="accepted"}`:          "2",
		`vehicle_positions_reports_last_hour{outcome="invalid"}`:           "0",
		`vehicle_positions_reports_last_hour{outcome="unauthorized"}`:      "0",
		`vehicle_positions_reports_last_hour{outcome="rate_limited"}`:      "0",
		`vehicle_positions_reports_last_hour{outcome="server_error"}`:      "0",
		`vehicle_positions_feed_fetches_total{outcome="served"}`:           "8",
		`vehicle_positions_feed_fetches_total{outcome="refused"}`:          "6",
		`vehicle_positions_feed_fetches_total{outcome="invalid"}`:          "6",
		`vehicle_positions_feed_fetches_total{outcome="server_error"}`:     "7",
		`vehicle_positions_feed_fetches_last_hour{outcome="served"}`:       "0",
		`vehicle_positions_feed_fetches_last_hour{outcome="refused"}`:      "1",
		`vehicle_positions_feed_fetches_last_hour{outcome="invalid"}`:      "0",
		`vehicle_positions_feed_fetches_last_hour{outcome="server_error"}`: "0",
		// The last full minute, as the Health page's tile counts it: bus-3.
		`vehicle_positions_vehicles_reporting`:            "1",
		`vehicle_positions_gtfs_loaded_timestamp_seconds`: strconv.FormatInt(loadedAt.Unix(), 10),
		`vehicle_positions_gtfs_routes`:                   strconv.Itoa(ix.Stats().Routes),
		`vehicle_positions_gtfs_trips`:                    strconv.Itoa(ix.Stats().Trips),
		`vehicle_positions_alerts{kind="bunched"}`:        "0",
		`vehicle_positions_alerts{kind="not_moving"}`:     "1",
		`vehicle_positions_alerts{kind="not_started"}`:    "0",
		`vehicle_positions_alerts{kind="off_route"}`:      "0",
		`vehicle_positions_alerts{kind="quiet"}`:          "2",
		`vehicle_positions_alerts_listed`:                 "1",
	}
	require.Positive(t, ix.Stats().Routes)
	require.Positive(t, ix.Stats().Trips)
	for series, want := range wantSamples {
		assert.Equal(t, want, m.value(t, series), series)
	}

	buildInfo := ""
	for series, value := range m.samples {
		if strings.HasPrefix(series, "vehicle_positions_build_info{") {
			require.Empty(t, buildInfo, "one build_info series")
			buildInfo = series
			assert.Equal(t, "1", value)
		}
	}
	assert.Contains(t, buildInfo, `goversion="`+runtime.Version()+`"`)
	assert.Contains(t, buildInfo, `revision="`)
	assert.Len(t, m.samples, len(wantSamples)+1, "no series beyond the ones listed")

	assert.Contains(t, m.body, "# HELP vehicle_positions_reports_total Position reports from driver phones since the server started, by outcome.\n# TYPE vehicle_positions_reports_total counter\n")
}

// TestMetrics_NoSchedule leaves the schedule's series out, rather than
// reporting a load at the Unix epoch.
func TestMetrics_NoSchedule(t *testing.T) {
	for name, catalog := range map[string]*gtfsCatalog{
		"no catalog":      nil,
		"no index loaded": newGTFSCatalog(func() *rider.Index { return nil }, rider.DefaultThresholds()),
	} {
		t.Run(name, func(t *testing.T) {
			m := scrape(t, metricsMux(t, newFeedHealth(time.Now), catalog, quietAlerts()))

			for family := range m.types {
				assert.False(t, strings.HasPrefix(family, "vehicle_positions_gtfs_"), "%s with no schedule", family)
			}
			assert.Equal(t, "0", m.value(t, `vehicle_positions_reports_total{outcome="accepted"}`))
		})
	}
}

// TestMetrics_AlertsUnreadable keeps the rest of the scrape when the alerts
// cannot be worked out, and says so in a series of its own: a failed scrape
// would read as the whole server down.
func TestMetrics_AlertsUnreadable(t *testing.T) {
	logs := captureLogs(t)
	health := newFeedHealth(time.Now)
	health.record(healthReports, http.StatusCreated)
	alerts := newAlertWatcher(&fakeTripLister{err: errors.New("database down")}, testAlertSettings)

	m := scrape(t, metricsMux(t, health, nil, alerts))

	assert.Equal(t, "0", m.value(t, "vehicle_positions_alerts_listed"))
	assert.NotContains(t, m.types, "vehicle_positions_alerts")
	assert.Equal(t, "1", m.value(t, `vehicle_positions_reports_total{outcome="accepted"}`))
	assert.Contains(t, logs.String(), "level=ERROR msg=\"metrics: failed to list alerts\"")
	assert.Contains(t, logs.String(), "database down")
}

// TestMetrics_AgreeWithTheHealthPage reads the same counts through the Health
// page and /metrics on one server, so the two cannot drift apart.
func TestMetrics_AgreeWithTheHealthPage(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	health := newFeedHealth(clock.now)
	// Distinct counts per outcome, so a label swapped in /metrics shows.
	statuses := []struct {
		endpoint healthEndpoint
		status   int
		times    int
	}{
		{healthReports, http.StatusCreated, 9},
		{healthReports, http.StatusConflict, 1},
		{healthReports, http.StatusForbidden, 2},
		{healthReports, http.StatusTooManyRequests, 3},
		{healthReports, http.StatusBadGateway, 4},
		{healthFeed, http.StatusOK, 8},
		{healthFeed, http.StatusUnauthorized, 5},
		{healthFeed, http.StatusBadRequest, 6},
		{healthFeed, http.StatusInternalServerError, 7},
	}
	for _, s := range statuses {
		for range s.times {
			health.record(s.endpoint, s.status)
		}
	}
	for _, vehicle := range []string{"bus-1", "bus-2", "bus-3"} {
		sendReport(t, health, liveReportPath, vehicle, http.StatusCreated)
	}
	// Push the first minute out of the hour, so the hour and the total differ.
	clock.advance(healthWindowMinutes * time.Minute)
	for range 2 {
		health.record(healthReports, http.StatusCreated)
	}
	health.record(healthFeed, http.StatusOK)
	sendReport(t, health, liveReportPath, "bus-1", http.StatusCreated)
	clock.advance(time.Minute)

	h := newHealthTestHandler(t, health)
	req := httptest.NewRequest(http.MethodGet, "/admin/health", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	page := w.Body.String()
	summary := healthTableRows(t, page, "health-summary")
	require.Len(t, summary, 9)

	m := scrape(t, h)

	series := func(family string) []string {
		return []string{
			m.value(t, `vehicle_positions_reports_`+family+`{outcome="accepted"}`),
			m.value(t, `vehicle_positions_reports_`+family+`{outcome="invalid"}`),
			m.value(t, `vehicle_positions_reports_`+family+`{outcome="unauthorized"}`),
			m.value(t, `vehicle_positions_reports_`+family+`{outcome="rate_limited"}`),
			m.value(t, `vehicle_positions_reports_`+family+`{outcome="server_error"}`),
			m.value(t, `vehicle_positions_feed_fetches_`+family+`{outcome="served"}`),
			m.value(t, `vehicle_positions_feed_fetches_`+family+`{outcome="refused"}`),
			m.value(t, `vehicle_positions_feed_fetches_`+family+`{outcome="invalid"}`),
			m.value(t, `vehicle_positions_feed_fetches_`+family+`{outcome="server_error"}`),
		}
	}
	lastHour, total := healthColumn(summary, 0), healthColumn(summary, 1)
	require.NotEqual(t, lastHour, total, "the fixture must tell the hour from the total")
	assert.Equal(t, lastHour, series("last_hour"))
	assert.Equal(t, total, series("total"))
	assert.Equal(t, []string{"15", "1", "2", "3", "4", "9", "5", "6", "7"}, total)

	vehicles := m.value(t, "vehicle_positions_vehicles_reporting")
	assert.Equal(t, "1", vehicles)
	assert.Contains(t, page, `<span class="tile-label">Vehicles reporting</span><span class="tile-value"><span class="count-up" data-count="`+vehicles+`">`)
}

// TestMetrics_NoSecrets fills the server with everything private it holds and
// checks none of it reaches the scrape: the counts are all /metrics carries.
func TestMetrics_NoSecrets(t *testing.T) {
	const gtfsKey = "gtfs-agency-key-5ecret"
	source := "https://gtfs.example.org/feed.zip?api_key=" + gtfsKey
	catalog, _ := fixtureCatalog(t, source, time.Now())

	now := time.Now()
	trips := &fakeTripLister{}
	alerts := newAlertWatcher(trips, testAlertSettings)
	alerts.watchingSince = now.Add(-time.Hour)
	alerts.record("bus-quiet", alertStop, now.Add(-5*time.Minute))
	trip := alertTrip("bus-quiet", now.Add(-time.Hour))
	trip.DriverName = "driver.private@agency.example"
	trips.trips = []TripSummary{trip}

	health := newFeedHealth(time.Now)
	sendReport(t, health, liveReportPath, "bus-quiet", http.StatusCreated)

	m := scrape(t, metricsMux(t, health, catalog, alerts))
	require.Equal(t, "1", m.value(t, `vehicle_positions_alerts{kind="quiet"}`), "the alert holding the driver's address must be counted")

	for _, secret := range []string{
		testMetricsToken, string(testSecret), gtfsKey, "gtfs.example.org",
		"driver.private@agency.example", "@", "bus-quiet", "Bus 7", "T1",
	} {
		assert.NotContains(t, m.body, secret)
	}
}

func TestMetricLabelEscaping(t *testing.T) {
	assert.Equal(t, `name="plain"`, metricLabel("name", "plain"))
	assert.Equal(t, `name="a\\b\"c\nd"`, metricLabel("name", "a\\b\"c\nd"))
}

func TestMetricsTokenCheck_DoesNotReachTheHandlerWhenRefused(t *testing.T) {
	reached := false
	h := requireMetricsToken(testMetricsToken, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	getMetrics(h, "Bearer wrong")
	assert.False(t, reached)

	getMetrics(h, "Bearer "+testMetricsToken)
	assert.True(t, reached)
}

// TestMetricsRoute_RefusalsAreNotLoggedEachTime sends a scanner's burst at
// /metrics through the real mux: one WARN line, not one per request.
func TestMetricsRoute_RefusalsAreNotLoggedEachTime(t *testing.T) {
	logs := captureLogs(t)
	mux := metricsMux(t, newFeedHealth(time.Now), nil, quietAlerts())

	for range 50 {
		require.Equal(t, http.StatusUnauthorized, getMetrics(mux, "Bearer wrong").Code)
	}

	assert.Equal(t, 1, strings.Count(logs.String(), "metrics request refused"), logs.String())
}

func TestMetricsRefusalLog(t *testing.T) {
	logs := captureLogs(t)
	clock := newHealthTestClock(healthTestStart)
	log := newMetricsRefusalLog(time.Minute, clock.now)
	lines := func() []string {
		var out []string
		for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
			if strings.Contains(line, "metrics request refused") {
				out = append(out, line)
			}
		}
		return out
	}

	log.refused("no bearer token", "198.51.100.1")
	require.Len(t, lines(), 1, "the first refusal is logged at once")
	assert.Contains(t, lines()[0], `level=WARN msg="metrics request refused" reason="no bearer token" ip=198.51.100.1 suppressed=0`)

	for range 3 {
		clock.advance(10 * time.Second)
		log.refused("wrong token", "198.51.100.2")
	}
	clock.advance(29 * time.Second)
	log.refused("wrong token", "198.51.100.3")
	assert.Len(t, lines(), 1, "nothing more within the minute")

	clock.advance(time.Second)
	log.refused("wrong token", "198.51.100.4")
	require.Len(t, lines(), 2, "a minute after the last line, the next refusal is logged")
	assert.Contains(t, lines()[1], `reason="wrong token" ip=198.51.100.4 suppressed=4`, "with the count of those left out")

	clock.advance(59 * time.Second)
	log.refused("wrong token", "198.51.100.5")
	clock.advance(time.Second)
	log.refused("wrong token", "198.51.100.6")
	require.Len(t, lines(), 3)
	assert.Contains(t, lines()[2], `ip=198.51.100.6 suppressed=1`, "the count starts again after each line")
}

// TestMetricsRefusalLog_Concurrent counts every refusal exactly once, logged
// or left out, when scrapes arrive together. Run with -race.
func TestMetricsRefusalLog_Concurrent(t *testing.T) {
	logs := captureLogs(t)
	clock := newHealthTestClock(healthTestStart)
	log := newMetricsRefusalLog(time.Minute, clock.now)
	h := requireMetricsTokenWith(testMetricsToken, false, log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			getMetrics(h, "Bearer wrong")
		}()
	}
	wg.Wait()
	clock.advance(time.Minute)
	log.refused("wrong token", "198.51.100.9")

	out := logs.String()
	assert.Equal(t, 2, strings.Count(out, "metrics request refused"), out)
	assert.Contains(t, out, "ip=198.51.100.9 suppressed=39")
}
