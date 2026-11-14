package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func reportsView(t *testing.T, r *serviceReporter, query string) (map[string]interface{}, int) {
	t.Helper()
	q, err := url.ParseQuery(query)
	require.NoError(t, err)
	data, status, err := reportsPageView(context.Background(), r, q)
	require.NoError(t, err)
	return data, status
}

// renderReportsContent renders the Reports page's content block, without the
// layout around it.
func renderReportsContent(t *testing.T, data map[string]interface{}) string {
	t.Helper()
	tmpl, err := template.ParseFS(files, "web/templates/views/reports.html")
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, tmpl.ExecuteTemplate(&buf, "content", data))
	return buf.String()
}

func TestReportsPage_Routes(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	data, status := reportsView(t, r, "date=2026-09-02")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "Wednesday 2 September 2026", data["DayLabel"])
	assert.Equal(t, "America/Los_Angeles", data["Timezone"])
	assert.Equal(t, "/api/v1/admin/reports/service?date=2026-09-02&format=csv", data["CSVURL"])
	assert.Equal(t, "/admin/reports?date=2026-09-01", data["PrevURL"])
	assert.Equal(t, "/admin/reports?date=2026-09-03", data["NextURL"])

	rows := data["Routes"].([]reportRouteRow)
	require.Len(t, rows, 3)
	assert.Equal(t, reportRouteRow{
		RouteID: "R1", Name: "1 Straight", Scheduled: 7, Ran: 5,
		OnTime: "64.3%", Early: 3, Late: 2, Unknown: "6.7%",
		TripsURL: "/admin/reports?date=2026-09-02&route_id=R1",
	}, rows[1])

	html := renderReportsContent(t, data)
	assert.Contains(t, html, "Wednesday 2 September 2026")
	assert.Contains(t, html, `href="/api/v1/admin/reports/service?date=2026-09-02&amp;format=csv"`)
	assert.Contains(t, html, "64.3%")
	assert.Contains(t, html, "3 early, 2 late")
	assert.Contains(t, html, `href="/admin/reports?date=2026-09-02&amp;route_id=R1"`)
}

func TestReportsPage_RouteTrips(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	data, status := reportsView(t, r, "date=2026-09-02&route_id=R1")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "1 Straight", data["Route"].(reportRouteRow).Name)
	assert.Equal(t, "/admin/reports?date=2026-09-01&route_id=R1", data["PrevURL"], "the day picker stays on the route")

	trips := data["Trips"].([]reportTripRow)
	require.Len(t, trips, 7)
	byID := make(map[string]reportTripRow, len(trips))
	for _, tr := range trips {
		byID[tr.GtfsTripID] = tr
	}

	t1 := byID["T1"]
	assert.Equal(t, "08:00 to 08:10", t1.Scheduled)
	assert.Equal(t, []reportRunRow{{Vehicle: "Bus T1", Driver: "Driver T1", TrailURL: "/admin/map?trip_id=1"}}, t1.Runs)
	assert.Equal(t, reportDeviation{Text: "0:06 late", Tone: "on-time"}, t1.Departure)
	assert.Equal(t, reportDeviation{Text: "0:16 early", Tone: "on-time"}, t1.Arrival)
	assert.Equal(t, "100.0%", t1.OnTime)
	assert.Equal(t, "", t1.Unknown, "nothing unknown, nothing to say")

	assert.Equal(t, reportDeviation{Text: "2:54 early", Tone: "early"}, byID["RE"].Departure)
	assert.Equal(t, reportDeviation{Text: "7:06 late", Tone: "late"}, byID["RL"].Departure)
	assert.Equal(t, reportDeviation{Text: "3:34 late", Tone: "on-time"}, byID["RL"].Arrival)
	assert.Equal(t, "33.3%", byID["RG"].Unknown)
	assert.Equal(t, "00:10 next day to 00:20 next day", byID["RM"].Scheduled)

	rn := byID["RN"]
	assert.Empty(t, rn.Runs)
	assert.Equal(t, reportDeviation{}, rn.Departure)
	assert.Equal(t, "", rn.OnTime)

	html := renderReportsContent(t, data)
	assert.Contains(t, html, `href="/admin/map?trip_id=1"`)
	assert.Contains(t, html, "View trail")
	assert.Contains(t, html, "Not run")
	assert.Contains(t, html, "2:54 early")
	assert.Contains(t, html, "&larr; All routes")
	assert.Contains(t, html, `name="route_id" value="R1"`)
}

func TestReportsPage_RouteWithNoServiceThatDay(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	data, status := reportsView(t, r, "date=2026-09-02&route_id=R9")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, true, data["NoRoute"])
	assert.Contains(t, renderReportsContent(t, data), "Route R9 has no trips scheduled on this day.")
}

func TestReportsPage_EmptyDay(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow.AddDate(0, 0, 10))

	data, status := reportsView(t, r, "date=2026-09-06")
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, data["Routes"])
	assert.Contains(t, renderReportsContent(t, data), "No trips scheduled on this day.")
}

func TestReportsPage_Errors(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)

	data, status := reportsView(t, r, "date=2-9-2026")
	assert.Equal(t, http.StatusBadRequest, status)
	html := renderReportsContent(t, data)
	assert.Contains(t, html, "Date must be YYYY-MM-DD.")
	assert.Contains(t, html, `value="2-9-2026"`, "the date picker keeps what was asked for")
	assert.NotContains(t, html, "Download CSV")

	noSchedule := newServiceReporter(store, func() *rider.Index { return nil }, testReportSettings)
	data, status = reportsView(t, noSchedule, "")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, renderReportsContent(t, data), "The GTFS schedule is not loaded")

	store.err = assert.AnError
	_, _, err := reportsPageView(context.Background(), r, url.Values{"date": {"2026-09-02"}})
	assert.ErrorIs(t, err, assert.AnError)
}

// TestReportsPage_MatchesJSONAndCSV: the page, the JSON and the CSV are one
// report, so every route's numbers are the same in all three.
func TestReportsPage_MatchesJSONAndCSV(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	var rep serviceReport
	jsonRec := getServiceReport(t, r, "?date=2026-09-02")
	require.Equal(t, http.StatusOK, jsonRec.Code)
	require.NoError(t, json.Unmarshal(jsonRec.Body.Bytes(), &rep))
	csvRec := getServiceReport(t, r, "?date=2026-09-02&format=csv")
	records, err := csv.NewReader(csvRec.Body).ReadAll()
	require.NoError(t, err)
	data, _ := reportsView(t, r, "date=2026-09-02")
	rows := data["Routes"].([]reportRouteRow)
	html := renderReportsContent(t, data)

	require.Len(t, rows, len(rep.Routes))
	require.Len(t, records, len(rep.Routes)+1)
	pct := func(p *float64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatFloat(*p, 'f', 1, 64) + "%"
	}
	withPercentSign := func(cell string) string {
		if cell == "" {
			return ""
		}
		return cell + "%"
	}
	for i, route := range rep.Routes {
		row, record := rows[i], records[i+1]
		assert.Equal(t, route.RouteID, row.RouteID)
		assert.Equal(t, route.RouteID, record[1])
		assert.Equal(t, route.TripsScheduled, row.Scheduled)
		assert.Equal(t, strconv.Itoa(route.TripsScheduled), record[4])
		assert.Equal(t, route.TripsRun, row.Ran)
		assert.Equal(t, strconv.Itoa(route.TripsRun), record[5])
		assert.Equal(t, pct(route.OnTimePercent), row.OnTime)
		assert.Equal(t, row.OnTime, withPercentSign(record[10]))
		assert.Equal(t, pct(route.UnknownPercent), row.Unknown)
		assert.Equal(t, row.Unknown, withPercentSign(record[11]))
		assert.Contains(t, html, row.OnTime)
		assert.Contains(t, html, row.Unknown)
	}
}

// newReportsHandler is the server's whole handler, admin UI on, with r as its
// reporter.
func newReportsHandler(t *testing.T, r *serviceReporter) http.Handler {
	t.Helper()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	h, err := newHandler(&noopStore{}, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, nil, nil, nil,
		newAlertWatcher(&noopStore{}, testAlertSettings), r)
	require.NoError(t, err)
	return h
}

func serve(h http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestReportsPage_SignedOut(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	h := newReportsHandler(t, r)

	page := serve(h, "/admin/reports?date=2026-09-02", nil)
	assert.Equal(t, http.StatusSeeOther, page.Code)
	assert.Equal(t, "/admin/login", page.Header().Get("Location"))

	api := serve(h, "/api/v1/admin/reports/service?date=2026-09-02&format=csv", nil)
	assert.Equal(t, http.StatusUnauthorized, api.Code)
	assert.Zero(t, store.reads, "nothing is worked out for a visitor who is not signed in")
}

func TestReportsPage_SignedIn(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)
	h := newReportsHandler(t, r)
	admin := cookieFor(t, "admin")

	rec := serve(h, "/admin/reports?date=2026-09-02", admin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "<title>Reports")
	assert.Regexp(t, `href="/admin/reports"\s+data-active="true"`, body, "the sidebar marks Reports as the page open")
	assert.Contains(t, body, "1 Straight")
	assert.Contains(t, body, "64.3%")

	rec = serve(h, "/admin/reports?date=2026-09-02&route_id=R1", admin)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `href="/admin/map?trip_id=1"`)

	rec = serve(h, "/admin/reports?date=13-11-2026", admin)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "Date must be YYYY-MM-DD.")

	// The page's Download CSV link goes to the API, which takes the same
	// session cookie.
	rec = serve(h, "/api/v1/admin/reports/service?date=2026-09-02&format=csv", admin)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/csv", rec.Header().Get("Content-Type"))
}

func TestReportsPage_DriverSession(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	h := newReportsHandler(t, r)

	page := serve(h, "/admin/reports", cookieFor(t, "driver"))
	assert.Equal(t, http.StatusSeeOther, page.Code)
	api := serve(h, "/api/v1/admin/reports/service", cookieFor(t, "driver"))
	assert.Equal(t, http.StatusForbidden, api.Code)
	assert.Zero(t, store.reads)
}
