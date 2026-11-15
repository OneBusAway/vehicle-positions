package main

import (
	"bytes"
	"encoding/json"
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

func TestNewHealthStrip(t *testing.T) {
	strip := newHealthStrip(healthPeriod{
		Reports: reportOutcomes{Accepted: 90, Invalid: 6, RateLimited: 4},
		Feed:    feedOutcomes{Served: 40, Refused: 7, Invalid: 2, ServerError: 1},
	})
	assert.Equal(t, healthStrip{
		ReportsAccepted: 90,
		ReportsRejected: 10,
		ErrorRate:       "10.0%",
		TopReason:       "invalid",
		FeedFetches:     50,
		FeedRefused:     7,
		FeedFailed:      3,
	}, strip)
}

func TestHealthPageData_NewestMinuteFirst(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)
	h.record(healthReports, http.StatusCreated)
	clock.advance(2 * time.Minute)
	h.record(healthReports, http.StatusBadRequest)

	data := healthPageData(h.snapshot())
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
// the layout, from the same snapshot the JSON endpoint serves.
func renderHealthContent(t *testing.T, snap feedHealthResponse) string {
	t.Helper()
	tmpl, err := template.ParseFS(files, "web/templates/views/health.html")
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, tmpl.ExecuteTemplate(&buf, "content", healthPageData(snap)))
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

	snap := h.snapshot()
	page := renderHealthContent(t, snap)

	summary := healthTableRows(t, page, "health-summary")
	require.Len(t, summary, 9)
	want := []string{
		healthCountText(snap.LastHour.Reports.Accepted), healthCountText(snap.LastHour.Reports.Invalid), healthCountText(snap.LastHour.Reports.Unauthorized),
		healthCountText(snap.LastHour.Reports.RateLimited), healthCountText(snap.LastHour.Reports.ServerError),
		healthCountText(snap.LastHour.Feed.Served), healthCountText(snap.LastHour.Feed.Refused), healthCountText(snap.LastHour.Feed.Invalid), healthCountText(snap.LastHour.Feed.ServerError),
	}
	assert.Equal(t, []string{"10", "1", "2", "3", "4", "8", "5", "6", "7"}, want)
	assert.Equal(t, want, healthColumn(summary, 1))
	assert.Equal(t, want, healthColumn(summary, 2), "the server started within the hour, so the total is the hour")

	rows := healthTableRows(t, page, "health-minutes")
	require.Len(t, rows, healthWindowMinutes)
	assert.Equal(t, []string{"09:16", "1", "0", "0", "0", "0", "0", "0", "0", "0"}, healthCellTexts(rows[0]))
	assert.Equal(t, []string{"09:15", "9", "1", "2", "3", "4", "8", "5", "6", "7"}, healthCellTexts(rows[1]))

	// The highlight marks a rejection in the minute it happened, and only then.
	assert.Contains(t, rows[1][2].class, "text-rose-700")
	assert.NotContains(t, rows[0][2].class, "text-rose-700")

	assert.Contains(t, page, "2026-11-14 09:15 UTC")
	assert.Contains(t, page, "50.0% of reports rejected in the last hour.")
}

func healthCountText(n uint64) string { return strconv.FormatUint(n, 10) }

func TestHealthPage_NoReports(t *testing.T) {
	page := renderHealthContent(t, newFeedHealth(newHealthTestClock(healthTestStart).now).snapshot())
	assert.Contains(t, page, "No reports in the last hour.")
	assert.Len(t, healthTableRows(t, page, "health-minutes"), healthWindowMinutes)
}

// newHealthTestHandler builds the full handler, admin UI included, around a
// feedHealth the test fills in.
func newHealthTestHandler(t *testing.T, health *feedHealth) http.Handler {
	t.Helper()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	h, err := newHandler(&noopStore{}, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, nil, nil, nil,
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
	assert.Equal(t, want, healthColumn(healthTableRows(t, page, "health-summary"), 1))
	assert.Contains(t, page, "22.2% of reports rejected in the last hour.")
}

func TestHealthPage_SignedOutIsRefused(t *testing.T) {
	health := newFeedHealth(newHealthTestClock(healthTestStart).now)
	h := newHealthTestHandler(t, health)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/health", nil))
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/login", w.Header().Get("Location"))

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NotContains(t, w.Body.String(), "last_hour")
}

func TestDashboard_FeedHealthStrip(t *testing.T) {
	tests := []struct {
		name     string
		record   func(h *feedHealth)
		contains []string
		absent   []string
	}{
		{
			name:   "nothing yet",
			record: func(h *feedHealth) {},
			contains: []string{
				`<span class="font-semibold text-slate-700">0</span> accepted`,
				`<span class="font-semibold text-slate-700">0</span> rejected</span>`,
				`<span class="font-semibold text-slate-700">0</span> refused key</span>`,
			},
			absent: []string{"mostly", "failed"},
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
				`<span class="font-semibold text-slate-700">3</span> accepted`,
				`<span class="font-semibold text-rose-700">1</span> rejected (25.0%, mostly rate limited)`,
				`Feed fetches, last hour: <span class="font-semibold text-slate-700">3</span>`,
				`<span class="font-semibold text-rose-700">1</span> refused key, <span class="font-semibold text-rose-700">1</span> failed`,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ui := newTestAdminUI(t)
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
			assert.Contains(t, body, `href="/admin/health"`)
			for _, s := range tc.contains {
				assert.Contains(t, body, s)
			}
			for _, s := range tc.absent {
				assert.NotContains(t, body, s)
			}
		})
	}
}

// The strip is the last hour, not the total since the server started.
func TestDashboard_FeedHealthStripIsTheLastHour(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	ui := newTestAdminUI(t)
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
	assert.Contains(t, body, `<span class="font-semibold text-slate-700">1</span> accepted`)
	assert.Contains(t, body, `<span class="font-semibold text-rose-700">1</span> rejected (50.0%, mostly invalid)`)
}
