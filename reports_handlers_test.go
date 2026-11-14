package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func getServiceReport(t *testing.T, r *serviceReporter, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/reports/service"+query, nil)
	rec := httptest.NewRecorder()
	handleServiceReport(r).ServeHTTP(rec, req)
	return rec
}

func TestHandleServiceReport_JSON(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	rec := getServiceReport(t, r, "?date=2026-09-02")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body struct {
		Date         string `json:"date"`
		Timezone     string `json:"timezone"`
		OnTimeEarlyS int    `json:"on_time_early_s"`
		OnTimeLateS  int    `json:"on_time_late_s"`
		Routes       []struct {
			RouteID        string   `json:"route_id"`
			TripsScheduled int      `json:"trips_scheduled"`
			TripsRun       int      `json:"trips_run"`
			OnTimePercent  *float64 `json:"on_time_percent"`
			UnknownPercent *float64 `json:"unknown_percent"`
			Trips          []struct {
				GtfsTripID          string            `json:"gtfs_trip_id"`
				ScheduledDeparture  string            `json:"scheduled_departure"`
				Runs                []json.RawMessage `json:"runs"`
				DepartureDeviationS *int              `json:"departure_deviation_s"`
			} `json:"trips"`
		} `json:"routes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "2026-09-02", body.Date)
	assert.Equal(t, "America/Los_Angeles", body.Timezone)
	assert.Equal(t, 60, body.OnTimeEarlyS)
	assert.Equal(t, 300, body.OnTimeLateS)
	require.Len(t, body.Routes, 3)
	r1 := body.Routes[1]
	assert.Equal(t, "R1", r1.RouteID)
	assert.Equal(t, 7, r1.TripsScheduled)
	assert.Equal(t, 5, r1.TripsRun)
	require.NotNil(t, r1.OnTimePercent)
	assert.Equal(t, 64.3, *r1.OnTimePercent)
	assert.Equal(t, "2026-09-02T08:00:00-07:00", r1.Trips[0].ScheduledDeparture, "times on the agency's clock")
	assert.Contains(t, rec.Body.String(), `"runs":[]`, "a trip not run has an empty list of runs")
	assert.Contains(t, rec.Body.String(), `"departure_deviation_s":null`)
}

func TestHandleServiceReport_CSVMatchesJSON(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	jsonRec := getServiceReport(t, r, "?date=2026-09-02")
	require.Equal(t, http.StatusOK, jsonRec.Code)
	var rep serviceReport
	require.NoError(t, json.Unmarshal(jsonRec.Body.Bytes(), &rep))

	rec := getServiceReport(t, r, "?date=2026-09-02&format=csv")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "text/csv", rec.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename="service-report-2026-09-02.csv"`, rec.Header().Get("Content-Disposition"))

	records, err := csv.NewReader(rec.Body).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 1+len(rep.Routes))
	assert.Equal(t, serviceReportCSVHeader, records[0])
	assert.Equal(t, []string{"2026-09-02", "R1", "1", "Straight", "7", "5", "9", "3", "2", "1", "64.3", "6.7"}, records[2])
	for i, route := range rep.Routes {
		assert.Equal(t, csvRow(rep.Date, route), records[i+1], "row %d", i+1)
	}
}

// csvRow is a JSON route row as the CSV should have it.
func csvRow(date string, r routeReport) []string {
	percent := func(p *float64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatFloat(*p, 'f', 1, 64)
	}
	return []string{date, r.RouteID, r.ShortName, r.LongName,
		strconv.Itoa(r.TripsScheduled), strconv.Itoa(r.TripsRun),
		strconv.Itoa(r.StopsOnTime), strconv.Itoa(r.StopsEarly), strconv.Itoa(r.StopsLate), strconv.Itoa(r.StopsUnknown),
		percent(r.OnTimePercent), percent(r.UnknownPercent)}
}

func TestHandleServiceReport_CSVEscapesFormulas(t *testing.T) {
	ix := fixtureIndexEdited(t, func(name string, body []byte) []byte {
		if name == "routes.txt" {
			return []byte(strings.Replace(string(body), "R1,A,1,Straight", "R1,A,=1,@Straight", 1))
		}
		return body
	})
	r := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return ix }, testReportSettings)

	rec := getServiceReport(t, r, "?date=2026-09-02&format=csv")
	require.Equal(t, http.StatusOK, rec.Code)
	records, err := csv.NewReader(rec.Body).ReadAll()
	require.NoError(t, err)
	var r1 []string
	for _, rec := range records {
		if rec[1] == "R1" {
			r1 = rec
		}
	}
	require.NotNil(t, r1)
	assert.Equal(t, "'=1", r1[2])
	assert.Equal(t, "'@Straight", r1[3])
}

func TestHandleServiceReport_EmptyDay(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow.AddDate(0, 0, 10))

	rec := getServiceReport(t, r, "?date=2026-09-06")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"routes":[]`)

	rec = getServiceReport(t, r, "?date=2026-09-06&format=csv")
	require.Equal(t, http.StatusOK, rec.Code)
	records, err := csv.NewReader(rec.Body).ReadAll()
	require.NoError(t, err)
	assert.Equal(t, [][]string{serviceReportCSVHeader}, records)
}

func TestHandleServiceReport_DefaultsToYesterday(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)

	rec := getServiceReport(t, r, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"date":"2026-09-03"`)
}

func TestHandleServiceReport_Errors(t *testing.T) {
	r, store, _ := newFixtureReporter(t, reportFixtureNow)
	cases := []struct {
		name    string
		r       *serviceReporter
		query   string
		status  int
		message string
	}{
		{"a date not in YYYY-MM-DD", r, "?date=2026-9-2", http.StatusBadRequest, "date must be YYYY-MM-DD"},
		{"a date that does not exist", r, "?date=2026-02-30", http.StatusBadRequest, "date must be YYYY-MM-DD"},
		{"a year too far out to read the days around", r, "?date=9999-12-31", http.StatusBadRequest, "date must be YYYY-MM-DD"},
		{"an unknown format", r, "?date=2026-09-02&format=xml", http.StatusBadRequest, "format must be json or csv"},
		{"no schedule loaded", newServiceReporter(store, func() *rider.Index { return nil }, testReportSettings), "?date=2026-09-02", http.StatusServiceUnavailable, "schedule data unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := getServiceReport(t, tc.r, tc.query)
			require.Equal(t, tc.status, rec.Code)
			var body map[string]string
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tc.message, body["error"])
		})
	}

	t.Run("a database error", func(t *testing.T) {
		store.err = errors.New("database down")
		t.Cleanup(func() { store.err = nil })
		rec := getServiceReport(t, r, "?date=2026-09-02")
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "database down")
	})
}
