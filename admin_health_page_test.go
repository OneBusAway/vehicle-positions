package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorRate(t *testing.T) {
	tests := []struct {
		name string
		r    reportOutcomes
		want string
	}{
		{"no reports", reportOutcomes{}, ""},
		{"none rejected", reportOutcomes{Accepted: 10}, "0.0%"},
		{"all rejected", reportOutcomes{Invalid: 3}, "100.0%"},
		{"every reason counts", reportOutcomes{Accepted: 36, Invalid: 1, Unauthorized: 1, RateLimited: 1, ServerError: 1}, "10.0%"},
		{"rounded to one place", reportOutcomes{Accepted: 2, ServerError: 1}, "33.3%"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, errorRate(tc.r))
		})
	}
}

func TestTopReason(t *testing.T) {
	tests := []struct {
		name string
		r    reportOutcomes
		want string
	}{
		{"nothing rejected", reportOutcomes{Accepted: 5}, ""},
		{"invalid", reportOutcomes{Invalid: 2, Unauthorized: 1}, "invalid"},
		{"unauthorized", reportOutcomes{Invalid: 1, Unauthorized: 2}, "signed out or not allowed"},
		{"rate limited", reportOutcomes{RateLimited: 3, ServerError: 2}, "rate limited"},
		{"server error", reportOutcomes{RateLimited: 2, ServerError: 3}, "server error"},
		{"a tie goes to the reason listed first", reportOutcomes{Unauthorized: 2, ServerError: 2}, "signed out or not allowed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, topReason(tc.r))
		})
	}
}

func TestNewHealthSummary(t *testing.T) {
	at := func(minute int) time.Time { return healthTestStart.Add(time.Duration(minute) * time.Minute) }
	summary := newHealthSummary(feedHealthResponse{
		LastHour: healthPeriod{
			Reports: reportOutcomes{Accepted: 1490, Invalid: 6, RateLimited: 4},
			Feed:    feedOutcomes{Served: 40, Refused: 7, Invalid: 2, ServerError: 1},
		},
		Minutes: []healthMinute{
			{Minute: at(0), Feed: feedOutcomes{Served: 3}, Vehicles: 9},
			{Minute: at(1), Feed: feedOutcomes{Served: 1, Refused: 2}, Vehicles: 8},
			{Minute: at(2), Feed: feedOutcomes{Refused: 5}, Vehicles: 2},
		},
	})
	assert.Equal(t, healthSummary{
		Vehicles:        8,
		VehiclesNote:    "Last full minute, at 09:16 UTC",
		ReportsAccepted: 1490,
		PerMinute:       "About 25 a minute",
		ReportsRejected: 10,
		ErrorRate:       "0.7%",
		TopReason:       "invalid",
		FeedServed:      40,
		LastFetch:       "09:16",
		FeedRefused:     7,
	}, summary, "the last fetch is the newest minute one was served in, not refused in; the vehicles are the last full minute's, not the one in progress")

	rate := func(accepted uint64) string {
		return newHealthSummary(feedHealthResponse{LastHour: healthPeriod{Reports: reportOutcomes{Accepted: accepted}}}).PerMinute
	}
	assert.Equal(t, "None in the last hour", rate(0))
	assert.Equal(t, "Under 1 a minute", rate(29))
	assert.Equal(t, "About 1 a minute", rate(30))
	assert.Equal(t, "About 2 a minute", rate(90))
	assert.Empty(t, newHealthSummary(feedHealthResponse{}).LastFetch)
}

func TestHealthHeadline(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    healthPeriod
		want string
	}{
		{"nothing at all", healthPeriod{}, "Nothing has come in during the last hour"},
		{"reports and fetches", healthPeriod{Reports: reportOutcomes{Accepted: 9}, Feed: feedOutcomes{Served: 3}}, "Reports and the feed are healthy"},
		{"a few turned away", healthPeriod{Reports: reportOutcomes{Accepted: 9, Invalid: 2, RateLimited: 2}}, "Reports and the feed are healthy"},
		{"as many turned away as taken", healthPeriod{Reports: reportOutcomes{Accepted: 4, Invalid: 4}}, "Reports and the feed are healthy"},
		{"more turned away than taken", healthPeriod{Reports: reportOutcomes{Accepted: 4, Unauthorized: 5}}, "Most reports were rejected in the last hour"},
		{"only refused fetches", healthPeriod{Feed: feedOutcomes{Refused: 3}}, "Reports and the feed are healthy"},
		{"one failed", healthPeriod{Reports: reportOutcomes{Accepted: 9}, Feed: feedOutcomes{ServerError: 1}}, "The server failed 1 request in the last hour"},
		{"several failed", healthPeriod{Reports: reportOutcomes{Accepted: 1, ServerError: 2, Invalid: 9}, Feed: feedOutcomes{ServerError: 1}}, "The server failed 3 requests in the last hour"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, healthHeadline(tc.p))
		})
	}
}

func TestHealthPageData_NewestMinuteFirst(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)
	h.record(healthReports, http.StatusCreated)
	clock.advance(2 * time.Minute)
	h.record(healthReports, http.StatusBadRequest)

	data := healthPageData(h.snapshot(), 1)
	rows, ok := data["Minutes"].([]healthMinuteRow)
	require.True(t, ok)
	require.Len(t, rows, healthWindowMinutes)
	assert.Equal(t, "09:17", rows[0].Minute)
	assert.Equal(t, reportOutcomes{Invalid: 1}, rows[0].Reports)
	assert.Equal(t, "09:16", rows[1].Minute)
	assert.Equal(t, "09:15", rows[2].Minute)
	assert.Equal(t, reportOutcomes{Accepted: 1}, rows[2].Reports)
	assert.Equal(t, "08:18", rows[healthWindowMinutes-1].Minute)
	assert.Equal(t, "2026-11-14 09:15 UTC", data["Since"])
}

// renderHealthContent renders the Feed health view's content block, without
// the layout, from the same snapshot the JSON endpoint serves, with the last
// 24 hours.
func renderHealthContent(t *testing.T, snap feedHealthResponse) string {
	t.Helper()
	tmpl, err := template.New("").Funcs(template.FuncMap{"asset": embeddedAssets(t).url}).ParseFS(files, "web/templates/layout/parts.html", "web/templates/views/health.html")
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, tmpl.ExecuteTemplate(&buf, "content", healthPageData(snap, 1)))
	return buf.String()
}

var (
	healthTableBody = regexp.MustCompile(`(?s)<table id="([a-z-]+)".*?<tbody[^>]*>(.*?)</tbody>`)
	healthTableRow  = regexp.MustCompile(`(?s)<tr>(.*?)</tr>`)
	healthTableCell = regexp.MustCompile(`(?s)<td class="([^"]*)">(.*?)</td>`)
)

// healthCell is one table cell: its text and its class attribute.
type healthCell struct {
	text, class string
}

// healthTableRows returns the body rows of the table with the given id.
func healthTableRows(t *testing.T, page, id string) [][]healthCell {
	t.Helper()
	for _, table := range healthTableBody.FindAllStringSubmatch(page, -1) {
		if table[1] != id {
			continue
		}
		var rows [][]healthCell
		for _, row := range healthTableRow.FindAllStringSubmatch(table[2], -1) {
			var cells []healthCell
			for _, cell := range healthTableCell.FindAllStringSubmatch(row[1], -1) {
				cells = append(cells, healthCell{text: strings.TrimSpace(cell[2]), class: cell[1]})
			}
			rows = append(rows, cells)
		}
		return rows
	}
	t.Fatalf("no table %q on the page", id)
	return nil
}

func healthCellTexts(cells []healthCell) []string {
	texts := make([]string, 0, len(cells))
	for _, c := range cells {
		texts = append(texts, c.text)
	}
	return texts
}

func healthColumn(rows [][]healthCell, i int) []string {
	texts := make([]string, 0, len(rows))
	for _, row := range rows {
		texts = append(texts, row[i].text)
	}
	return texts
}

func TestHealthPage_AgreesWithTheJSON(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)
	// Distinct counts per outcome, so a column swapped in the template shows.
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
			h.record(s.endpoint, s.status)
		}
	}
	// One more report in the next minute, so the hour and the total differ
	// from any single minute.
	clock.advance(time.Minute)
	h.record(healthReports, http.StatusCreated)

	snap := h.report(context.Background(), 1)
	page := renderHealthContent(t, snap)

	summary := healthTableRows(t, page, "health-summary")
	require.Len(t, summary, 9)
	want := []string{
		healthCountText(snap.LastHour.Reports.Accepted), healthCountText(snap.LastHour.Reports.Invalid), healthCountText(snap.LastHour.Reports.Unauthorized),
		healthCountText(snap.LastHour.Reports.RateLimited), healthCountText(snap.LastHour.Reports.ServerError),
		healthCountText(snap.LastHour.Feed.Served), healthCountText(snap.LastHour.Feed.Refused), healthCountText(snap.LastHour.Feed.Invalid), healthCountText(snap.LastHour.Feed.ServerError),
	}
	assert.Equal(t, []string{"10", "1", "2", "3", "4", "8", "5", "6", "7"}, want)
	// The outcome's name has no class, so the two counts are the cells read.
	assert.Equal(t, want, healthColumn(summary, 0))
	assert.Equal(t, want, healthColumn(summary, 1), "the hour is the only one of the day with counts")
	assert.Equal(t, want, healthColumn(summary, 2), "the server started within the hour, so the total is the hour")

	rows := healthTableRows(t, page, "health-minutes")
	require.Len(t, rows, healthWindowMinutes)
	assert.Equal(t, []string{"09:16", "1", "0", "0", "0", "0", "0", "0", "0", "0"}, healthCellTexts(rows[0]))
	assert.Equal(t, []string{"09:15", "9", "1", "2", "3", "4", "8", "5", "6", "7"}, healthCellTexts(rows[1]))

	// A rejection stands out in the minute it happened, and only then.
	assert.Equal(t, "num right tone-warn strong", rows[1][2].class)
	assert.Equal(t, "num right muted", rows[0][2].class)
	assert.Contains(t, page, `<span class="tile-value tone-warn"><span class="count-up" data-count="10">10</span>`)

	assert.Contains(t, page, "2026-11-14 09:15 UTC")
	assert.Contains(t, page, `<h2 class="h1">The server failed 11 requests in the last hour</h2>`)
	assert.Contains(t, page, `<span class="count-up" data-count="10">10</span></span><span class="tile-note">Under 1 a minute</span>`)
	assert.Contains(t, page, `<span class="count-up" data-count="10">10</span></span><span class="tile-note">50.0% of reports, mostly server error</span>`)
	assert.Contains(t, page, `<span class="count-up" data-count="8">8</span></span><span class="tile-note">Last one at 09:15 UTC</span>`)
	assert.Contains(t, page, `<span class="count-up" data-count="5">5</span></span><span class="tile-note">Missing or bad API key</span>`)
	assert.Contains(t, page, `role="img" aria-label="Location reports a minute over the last hour: 60 bars, the highest 19"`)
	assert.Contains(t, page, `data-tip="09:15|9 accepted, 10 rejected"`)
	assert.Contains(t, page, `role="img" aria-label="Feed fetches served a minute over the last hour: 60 bars, the highest 8"`)
	assert.Contains(t, page, `data-tip="09:15|8 served"`)
	assert.Contains(t, page, "Most in a minute: 19")
}

func healthCountText(n uint64) string { return strconv.FormatUint(n, 10) }

func TestHealthPage_NoReports(t *testing.T) {
	page := renderHealthContent(t, newFeedHealth(newHealthTestClock(healthTestStart).now).snapshot())
	assert.Contains(t, page, `<h2 class="h1">Nothing has come in during the last hour</h2>`)
	assert.Contains(t, page, `<span class="tile-label">Vehicles reporting</span><span class="tile-value"><span class="count-up" data-count="0">0</span></span><span class="tile-note">None in the last hour</span>`)
	assert.Contains(t, page, `aria-label="Vehicles reporting a minute over the last hour: 60 bars, the highest 0"`, "an empty hour still draws the chart")
	assert.Contains(t, page, `<span class="tile-value"><span class="count-up" data-count="0">0</span></span><span class="tile-note">No reports in the last hour</span>`, "nothing rejected is not amber")
	assert.Contains(t, page, `<span class="tile-note">None in the last hour</span>`)
	assert.Len(t, healthTableRows(t, page, "health-minutes"), healthWindowMinutes)
}

// TestHealthPage_VehiclesReporting: the tile is the last full minute's count
// and the chart the hour's, both from the vehicles whose reports were
// accepted. A restarted server starts the minutes again.
func TestHealthPage_VehiclesReporting(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	health := newFeedHealth(clock.now)
	for _, vehicle := range []string{"bus-1", "bus-2", "bus-3", "bus-1"} {
		sendReport(t, health, liveReportPath, vehicle, http.StatusCreated)
	}
	clock.advance(time.Minute)
	sendReport(t, health, liveReportPath, "bus-1", http.StatusCreated)

	page := renderHealthContent(t, health.snapshot())
	assert.Contains(t, page, `<span class="tile-label">Vehicles reporting</span><span class="tile-value"><span class="count-up" data-count="3">3</span></span><span class="tile-note">Last full minute, at 09:15 UTC</span>`)
	assert.Contains(t, page, `<h3 id="health-vehicles" style="flex:1">Vehicles reporting per minute</h3>`)
	assert.Contains(t, page, `aria-label="Vehicles reporting a minute over the last hour: 60 bars, the highest 3"`)
	assert.Contains(t, page, `data-tip="09:15|3 reporting"`)
	assert.Contains(t, page, `data-tip="09:16|1 reporting"`)
	assert.Contains(t, page, `data-tip="09:14|0 reporting"`)
	assert.Contains(t, page, "Most in a minute: 3")
	assert.Contains(t, page, "Each hour is saved when it closes, so the last day is kept through a restart; the totals count from the server's start at")
	assert.NotContains(t, page, "A restart starts the counts again.")

	restarted := renderHealthContent(t, newFeedHealth(clock.now).snapshot())
	assert.Contains(t, restarted, `<span class="tile-label">Vehicles reporting</span><span class="tile-value"><span class="count-up" data-count="0">0</span></span><span class="tile-note">None in the last hour</span>`)
	assert.NotContains(t, restarted, `|1 reporting"`)
}

// newHealthTestHandler builds the full handler, admin UI and /metrics
// included, around a feedHealth the test fills in.
func newHealthTestHandler(t *testing.T, health *feedHealth) http.Handler {
	t.Helper()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	h, err := newHandler(&noopStore{}, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, testMetricsToken, nil, nil, nil,
		newAlertWatcher(&noopStore{}, testAlertSettings), nil, health)
	require.NoError(t, err)
	return h
}

func TestHealthPage_ServedPageAgreesWithTheAPI(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	health := newFeedHealth(clock.now)
	for range 7 {
		health.record(healthReports, http.StatusCreated)
	}
	health.record(healthReports, http.StatusConflict)
	health.record(healthReports, http.StatusInternalServerError)
	health.record(healthFeed, http.StatusUnauthorized)
	h := newHealthTestHandler(t, health)

	req := httptest.NewRequest(http.MethodGet, "/admin/health", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, healthPageRefresh, w.Header().Get("Refresh"))
	page := w.Body.String()
	assert.Contains(t, page, "<title>Feed health")

	adminToken, err := generateJWT(&User{ID: 2, Email: "admin@test.com", Role: "admin"}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var api feedHealthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &api))

	p := api.LastHour
	want := []string{
		healthCountText(p.Reports.Accepted), healthCountText(p.Reports.Invalid), healthCountText(p.Reports.Unauthorized),
		healthCountText(p.Reports.RateLimited), healthCountText(p.Reports.ServerError),
		healthCountText(p.Feed.Served), healthCountText(p.Feed.Refused), healthCountText(p.Feed.Invalid), healthCountText(p.Feed.ServerError),
	}
	require.Equal(t, []string{"7", "1", "0", "0", "1", "0", "1", "0", "0"}, want)
	assert.Equal(t, want, healthColumn(healthTableRows(t, page, "health-summary"), 0))
	assert.Contains(t, page, "22.2% of reports, mostly invalid")
	assert.Contains(t, page, `<h1 class="topbar-title">Feed health</h1>`)
	assert.Contains(t, page, `<h2 class="h1">The server failed 1 request in the last hour</h2>`)
	assert.Contains(t, page, `<div class="tip" id="tip" aria-hidden="true"></div>`)
}

func TestHealthPage_SignedOutIsRefused(t *testing.T) {
	health := newFeedHealth(newHealthTestClock(healthTestStart).now)
	h := newHealthTestHandler(t, health)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/health", nil))
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, loginLocation(http.MethodGet, "/admin/health"), w.Header().Get("Location"))

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NotContains(t, w.Body.String(), "last_hour")
}

// TestDashboard_FeedHealth: the Overview's report card and its Feed line are
// the last hour's counts, with a way through to the Feed health page.
func TestDashboard_FeedHealth(t *testing.T) {
	tests := []struct {
		name     string
		record   func(h *feedHealth)
		contains []string
	}{
		{
			name:   "nothing yet",
			record: func(h *feedHealth) {},
			contains: []string{
				`</i>0 accepted</span>`,
				`</i>0 rejected</span>`,
				`No fetches in the last hour`,
				`<span class="pill plain">Idle</span>`,
			},
		},
		{
			name: "rejections and refused fetches",
			record: func(h *feedHealth) {
				for range 3 {
					h.record(healthReports, http.StatusCreated)
				}
				h.record(healthReports, http.StatusTooManyRequests)
				h.record(healthFeed, http.StatusOK)
				h.record(healthFeed, http.StatusUnauthorized)
				h.record(healthFeed, http.StatusInternalServerError)
			},
			contains: []string{
				`</i>3 accepted</span>`,
				`</i>1 rejected</span>`,
				`Most in a minute: 4`,
				`data-tip="` + healthTestStart.UTC().Format("15:04") + `|3 accepted, 1 rejected"`,
				`1 fetch served in the last hour, last at ` + healthTestStart.UTC().Format("15:04"),
				`<span class="pill pill-warn">Errors</span>`,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ui := newTestAdminUI(t)
			ui.stats = &fakeAdminStats{vehicles: 1, drivers: 1}
			ui.health = newFeedHealth(newHealthTestClock(healthTestStart).now)
			tc.record(ui.health)
			mux := http.NewServeMux()
			registerAdminUI(mux, ui)

			req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
			req.AddCookie(cookieFor(t, "admin"))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)

			body := w.Body.String()
			assert.Contains(t, body, `<a class="line-row system" href="/admin/health">`)
			for _, s := range tc.contains {
				assert.Contains(t, body, s)
			}
		})
	}
}

// The report card is the last hour, not the total since the server started.
func TestDashboard_FeedHealthIsTheLastHour(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	ui := newTestAdminUI(t)
	ui.stats = &fakeAdminStats{vehicles: 1, drivers: 1}
	ui.health = newFeedHealth(clock.now)
	for range 5 {
		ui.health.record(healthReports, http.StatusCreated)
	}
	clock.advance(healthWindowMinutes * time.Minute)
	ui.health.record(healthReports, http.StatusCreated)
	ui.health.record(healthReports, http.StatusBadRequest)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()
	assert.Contains(t, body, `</i>1 accepted</span>`)
	assert.Contains(t, body, `</i>1 rejected</span>`)
}

// TestHealthPage_LastDay: the hours section draws the last 24 hours from the
// saved rows and the hour in progress from memory, every hour with a bar and
// a row, an hour with nothing saved at zero.
func TestHealthPage_LastDay(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	earlier := healthTestHour.Add(-2 * time.Hour)
	store.rows[earlier] = healthHour{
		Hour:     earlier,
		Reports:  reportOutcomes{Accepted: 300, Invalid: 4, Unauthorized: 1},
		Feed:     feedOutcomes{Served: 120, Refused: 2},
		Vehicles: 6,
	}
	h := newStoredFeedHealth(t, clock, store)
	sendReport(t, h, liveReportPath, "bus-1", http.StatusCreated)
	h.record(healthFeed, http.StatusOK)

	page := renderHealthContent(t, h.report(context.Background(), 1))

	assert.Contains(t, page, `<h3 id="health-hours" class="h2" style="flex:1;margin:0">Last 24 hours</h3>`)
	assert.Contains(t, page, `<a href="/admin/health" aria-current="true">24 hours</a><a href="/admin/health?days=7">7 days</a>`)
	assert.Contains(t, page, `aria-label="Location reports an hour over the last 24 hours: 24 bars, the highest 305"`)
	assert.Contains(t, page, `data-tip="07:00|300 accepted, 5 rejected"`)
	assert.Contains(t, page, `data-tip="08:00|0 accepted"`, "an hour with no row is a zero bar")
	assert.Contains(t, page, `data-tip="09:00|1 accepted"`, "the hour in progress, from memory")
	assert.Contains(t, page, `aria-label="Vehicles reporting an hour over the last 24 hours: 24 bars, the highest 6"`)
	assert.Contains(t, page, `data-tip="07:00|6 reporting"`)
	assert.Contains(t, page, `aria-label="Feed fetches served an hour over the last 24 hours: 24 bars, the highest 120"`)
	assert.Contains(t, page, `data-tip="09:00|1 served"`)
	assert.Contains(t, page, "Show the last 24 hours hour by hour")

	rows := healthTableRows(t, page, "health-hours-table")
	require.Len(t, rows, healthDayHours)
	assert.Equal(t, []string{"09:00", "1", "1", "0", "0", "0", "0", "1", "0", "0", "0"}, healthCellTexts(rows[0]), "newest first")
	assert.Equal(t, []string{"08:00", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0"}, healthCellTexts(rows[1]))
	assert.Equal(t, []string{"07:00", "6", "300", "4", "1", "0", "0", "120", "2", "0", "0"}, healthCellTexts(rows[2]))
	assert.Equal(t, "num right tone-warn strong", rows[2][3].class, "a rejection stands out in its hour")
	assert.Equal(t, "10:00", healthCellTexts(rows[healthDayHours-1])[0])

	summary := healthTableRows(t, page, "health-summary")
	assert.Equal(t, []string{"301", "4", "1", "0", "0", "121", "2", "0", "0"}, healthColumn(summary, 1), "the day's outcomes")
	assert.Contains(t, page, `<th class="right">Last 24 hours</th>`)
}

func TestHealthPage_LastWeek(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	daysAgo := healthTestHour.Add(-3 * 24 * time.Hour)
	store.rows[daysAgo] = healthHour{Hour: daysAgo, Reports: reportOutcomes{Accepted: 50}}
	h := newStoredFeedHealth(t, clock, store)

	tmpl, err := template.New("").Funcs(template.FuncMap{"asset": embeddedAssets(t).url}).ParseFS(files, "web/templates/layout/parts.html", "web/templates/views/health.html")
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, tmpl.ExecuteTemplate(&buf, "content", healthPageData(h.report(context.Background(), 7), 7)))
	page := buf.String()

	assert.Contains(t, page, `<h3 id="health-hours" class="h2" style="flex:1;margin:0">Last 7 days</h3>`)
	assert.Contains(t, page, `<a href="/admin/health">24 hours</a><a href="/admin/health?days=7" aria-current="true">7 days</a>`)
	assert.Contains(t, page, `aria-label="Location reports an hour over the last 7 days: 168 bars, the highest 50"`)
	assert.Contains(t, page, `data-tip="Nov 11 09:00|50 accepted"`, "a week's hours name their day")
	assert.Len(t, healthTableRows(t, page, "health-hours-table"), healthWeekHours)
	assert.Contains(t, page, `<th class="right">Last 7 days</th>`)
}

func TestHealthPage_HoursUnreadable(t *testing.T) {
	store := newFakeHourStore()
	h := newStoredFeedHealth(t, newHealthTestClock(healthTestStart), store)
	h.record(healthReports, http.StatusCreated)
	store.listErr = errors.New("connection refused")

	page := renderHealthContent(t, h.report(context.Background(), 1))

	assert.Contains(t, page, "The hourly counts could not be read from the database.")
	assert.NotContains(t, page, `id="health-hours-table"`)
	assert.Contains(t, page, `<h2 class="h1">Reports and the feed are healthy</h2>`, "the last hour still shows")
	assert.Equal(t, []string{"1", "0", "0", "0", "0", "0", "0", "0", "0"}, healthColumn(healthTableRows(t, page, "health-summary"), 0))
}

func TestHealthPage_DaysFromTheQuery(t *testing.T) {
	health := newFeedHealth(newHealthTestClock(healthTestStart).now)
	h := newHealthTestHandler(t, health)

	for query, want := range map[string]string{
		"?days=7":  "Last 7 days",
		"?days=30": "Last 24 hours",
		"":         "Last 24 hours",
	} {
		req := httptest.NewRequest(http.MethodGet, "/admin/health"+query, nil)
		req.AddCookie(cookieFor(t, "admin"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, query)
		assert.Contains(t, w.Body.String(), `<h3 id="health-hours" class="h2" style="flex:1;margin:0">`+want+`</h3>`, query)
	}
}
