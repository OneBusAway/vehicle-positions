package main

import (
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// newTripDetailMux serves the admin UI with store's trip and r's schedule.
func newTripDetailMux(t *testing.T, store TripTrailStore, r *serviceReporter) *http.ServeMux {
	t.Helper()
	ui := newTestAdminUI(t)
	ui.tripTrails = store
	ui.reports = r
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	return mux
}

// fixtureTrip is fixture trip id from the report fixture day, with its trail.
func fixtureTrip(t *testing.T, ix *rider.Index, id int64) (TripSummary, []LocationPoint) {
	t.Helper()
	trips, trails := reportFixtureTrips(t, ix)
	for _, trip := range trips {
		if trip.ID == id {
			return trip, trails[id]
		}
	}
	t.Fatalf("no fixture trip %d", id)
	return TripSummary{}, nil
}

var tripDataBlob = regexp.MustCompile(`<script type="application/json" id="trip-data">(.*?)</script>`)

// pageTripMap is what the page hands its script.
func pageTripMap(t *testing.T, body string) tripMap {
	t.Helper()
	m := tripDataBlob.FindStringSubmatch(body)
	require.NotNil(t, m, "the page carries the map's data")
	var data tripMap
	require.NoError(t, json.Unmarshal([]byte(m[1]), &data))
	return data
}

func TestTripDetailPage_RequiresAdminSession(t *testing.T) {
	r, _, ix := newFixtureReporter(t, reportFixtureNow)
	trip, points := fixtureTrip(t, ix, 1)
	mux := newTripDetailMux(t, &fakeTripTrailStore{trip: &trip, points: points}, r)

	for _, role := range []string{"", "driver"} {
		req := httptest.NewRequest(http.MethodGet, "/admin/trips/1", nil)
		if role != "" {
			req.AddCookie(cookieFor(t, role))
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusSeeOther, w.Code, "role %q", role)
		// Only the path: the login page may carry where to return to.
		to, err := url.Parse(w.Header().Get("Location"))
		require.NoError(t, err)
		assert.Equal(t, "/admin/login", to.Path, "role %q", role)
	}

	assert.Contains(t, getAdminPage(t, mux, "/admin/trips/1"), "Stop ST1", "an admin gets the page")
}

func TestTripDetailPage_UnknownTrip(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)
	mux := newTripDetailMux(t, &fakeTripTrailStore{tripErr: ErrTripNotFound}, r)

	for _, path := range []string{"/admin/trips/999", "/admin/trips/not-a-number"} {
		assert.Equal(t, http.StatusNotFound, requestAdminPage(t, mux, path).Code, path)
	}
}

func TestTripDetailPage_StoreErrors(t *testing.T) {
	r, _, _ := newFixtureReporter(t, reportFixtureNow)
	trip := &TripSummary{ID: 1, VehicleID: "bus-1", Status: "completed"}
	for name, store := range map[string]*fakeTripTrailStore{
		"trip":  {tripErr: errors.New("boom")},
		"trail": {trip: trip, pointsErr: errors.New("boom")},
	} {
		w := requestAdminPage(t, newTripDetailMux(t, store, r), "/admin/trips/1")
		assert.Equal(t, http.StatusInternalServerError, w.Code, name)
		assert.Contains(t, w.Body.String(), "internal server error", name)
	}
}

// TestTripDetail_AgreesWithTheReportsPage judges each run of the report
// fixture day on its own page and on the Reports page. Every scheduled trip
// that day ran once, so the two must agree: the page's summary is the Reports
// row itself, and the stop rows must reach the same first and last stop times
// and the same on-time and unknown shares.
func TestTripDetail_AgreesWithTheReportsPage(t *testing.T) {
	r, store, ix := newFixtureReporter(t, reportFixtureNow)

	reportRows := make(map[string]reportTripRow)
	for _, route := range []string{"R1", "R2", "R3"} {
		data, status := reportsView(t, r, "date=2026-09-02&route_id="+route)
		require.Equal(t, http.StatusOK, status)
		for _, row := range data["Trips"].([]reportTripRow) {
			reportRows[row.GtfsTripID] = row
		}
	}

	for _, trip := range store.trips[:len(reportFixtureRuns)] {
		t.Run(trip.GtfsTripID, func(t *testing.T) {
			want, ok := reportRows[trip.GtfsTripID]
			require.True(t, ok)
			require.Len(t, want.Runs, 1, "the fixture runs each trip once")

			d := newTripDetail(trip, store.trails[trip.ID], ix, testReportSettings, 5*time.Minute, reportFixtureNow)
			require.True(t, d.Timetable)
			assert.Equal(t, want, d.Summary)

			require.NotEmpty(t, d.Stops)
			assert.Equal(t, want.Departure, d.Stops[0].Deviation, "first stop")
			assert.Equal(t, want.Arrival, d.Stops[len(d.Stops)-1].Deviation, "last stop")

			var counts stopCounts
			for _, row := range d.Stops {
				require.True(t, row.Timed, "the fixture marks no timepoints, so every stop counts")
				switch row.Deviation.Tone {
				case "on-time":
					counts.add(stopOnTime)
				case "early":
					counts.add(stopEarly)
				case "late":
					counts.add(stopLate)
				default:
					counts.add(stopUnknown)
				}
			}
			assert.Equal(t, want.OnTime, formatPercent(counts.onTimePercent()), "on time")
			if want.Unknown != "" {
				assert.Equal(t, want.Unknown, formatPercent(counts.unknownPercent()), "unknown")
			} else {
				assert.Zero(t, counts.unknown)
			}
		})
	}
}

func TestTripDetail_TimetabledTrip(t *testing.T) {
	ix := reportFixtureIndex(t)

	// T1 waits at ST1 and leaves on time, is 3:16 late leaving ST2, and
	// reaches ST3 16 s early.
	trip, points := fixtureTrip(t, ix, 1)
	d := newTripDetail(trip, points, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	assert.Equal(t, "America/Los_Angeles", d.Timezone)
	assert.Equal(t, "2026-09-02 07:57 PDT", d.Start)
	assert.Equal(t, tripRoute{ID: "R1", Name: "1", LongName: "Straight", Color: "#0077C0", TextColor: "#FFFFFF"}, d.Route)
	assert.Equal(t, "#0077C0", d.Map.Color, "the trail is in the route's colour")
	assert.Equal(t, "North", d.Headsign)
	assert.Empty(t, d.Note)
	assert.Equal(t, []tripStopRow{
		{Sequence: 1, Name: "Stop ST1", Scheduled: "08:00", Passed: "08:00:06", Deviation: reportDeviation{Text: "0:06 late", Tone: "on-time"}, Timed: true},
		{Sequence: 2, Name: "Stop ST2", Scheduled: "08:05", Passed: "08:08:16", Deviation: reportDeviation{Text: "3:16 late", Tone: "on-time"}, Timed: true},
		{Sequence: 3, Name: "Stop ST3", Scheduled: "08:10", Passed: "08:09:44", Deviation: reportDeviation{Text: "0:16 early", Tone: "on-time"}, Timed: true},
	}, d.Stops)
	assert.Len(t, d.Map.Stops, 3)
	assert.Len(t, d.Map.Shape, 3, "S1's three points")

	// RG's phone is silent around ST2, so its time there is unknown.
	trip, points = fixtureTrip(t, ix, 4)
	require.Equal(t, "RG", trip.GtfsTripID)
	d = newTripDetail(trip, points, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	assert.Equal(t, tripStopRow{Sequence: 2, Name: "Stop ST2", Scheduled: "13:05", Deviation: reportDeviation{Text: "unknown", Tone: "unknown"}, Timed: true}, d.Stops[1])

	// RM runs after midnight on the service day before the calendar day.
	trip, points = fixtureTrip(t, ix, 5)
	require.Equal(t, "RM", trip.GtfsTripID)
	d = newTripDetail(trip, points, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	assert.Equal(t, "00:10 next day", d.Stops[0].Scheduled)
	assert.Equal(t, "00:10:06 next day", d.Stops[0].Passed)
	assert.Equal(t, "0:06 late", d.Stops[0].Deviation.Text)
}

// TestTripDetail_UntimedStop marks ST2 as no timepoint: the page still shows
// when the vehicle left it, but, like the Reports page, leaves it out of the
// on-time share.
func TestTripDetail_UntimedStop(t *testing.T) {
	ix := fixtureIndexEdited(t, func(name string, body []byte) []byte {
		if name != "stop_times.txt" {
			return body
		}
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		for i, line := range lines {
			switch {
			case i == 0:
				lines[i] = line + ",timepoint"
			case strings.HasPrefix(line, "T1,") && strings.Contains(line, ",ST2,"):
				lines[i] = line + ",0"
			default:
				lines[i] = line + ",1"
			}
		}
		return []byte(strings.Join(lines, "\n") + "\n")
	})
	trip := mustTrip(t, ix, "T1")
	// Seven minutes late at ST1 and ST2, 3:34 late at ST3.
	points := driveTrip(trip, reportDayStart, straightLegs(hms(8, 0, 0), hms(8, 7, 0), hms(8, 12, 0))(trip), nil)
	summary := TripSummary{ID: 3, VehicleID: "bus-1", RouteID: "R1", GtfsTripID: "T1", StartTime: reportDayStart.Add(hms(8, 0, 0)), Status: "completed"}

	d := newTripDetail(summary, points, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	require.Len(t, d.Stops, 3)
	assert.True(t, d.Stops[0].Timed)
	assert.False(t, d.Stops[1].Timed)
	assert.True(t, d.Stops[2].Timed)
	assert.Equal(t, "7:06 late", d.Stops[1].Deviation.Text, "an untimed stop still shows how far off it was")
	assert.Equal(t, "50.0%", d.Summary.OnTime, "late at ST1, on time at ST3, and ST2 not counted")
}

// TestTripDetail_RunningTripStopsAhead is T1 still running at 08:01, between
// ST1 and ST2: the stops it has not got to are ahead of it, not unknown.
func TestTripDetail_RunningTripStopsAhead(t *testing.T) {
	ix := reportFixtureIndex(t)
	trip, points := fixtureTrip(t, ix, 1)
	var sofar []LocationPoint
	for _, p := range points {
		if p.Timestamp <= reportDayStart.Add(hms(8, 1, 0)).Unix() {
			sofar = append(sofar, p)
		}
	}
	trip.Status = "active"

	d := newTripDetail(trip, sofar, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	require.Len(t, d.Stops, 3)
	assert.Equal(t, "08:00:06", d.Stops[0].Passed)
	assert.False(t, d.Stops[0].Ahead)
	for _, row := range d.Stops[1:] {
		assert.True(t, row.Ahead, row.Name)
		assert.Empty(t, row.Passed, row.Name)
		assert.Equal(t, reportDeviation{}, row.Deviation, row.Name)
		assert.NotEmpty(t, row.Scheduled, row.Name)
	}
	assert.Equal(t, "100.0%", d.Summary.OnTime)

	body := renderTripDetailContent(t, d)
	assert.Contains(t, body, "On time so far at")
	assert.Contains(t, body, "not yet")
	assert.NotContains(t, body, ">unknown<")

	// The same reports from a finished trip: what it never reached is unknown.
	trip.Status = "completed"
	d = newTripDetail(trip, sofar, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	for _, row := range d.Stops[1:] {
		assert.False(t, row.Ahead, row.Name)
		assert.Equal(t, "unknown", row.Deviation.Tone, row.Name)
	}

	// RG at 13:09:30, still running: its phone was silent as it left ST2,
	// which it is now past, so ST2 is unknown and only ST3 is ahead.
	trip, points = fixtureTrip(t, ix, 4)
	require.Equal(t, "RG", trip.GtfsTripID)
	sofar = nil
	for _, p := range points {
		if p.Timestamp <= reportDayStart.Add(hms(13, 9, 30)).Unix() {
			sofar = append(sofar, p)
		}
	}
	trip.Status = "active"
	d = newTripDetail(trip, sofar, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	assert.False(t, d.Stops[1].Ahead)
	assert.Equal(t, "unknown", d.Stops[1].Deviation.Tone)
	assert.True(t, d.Stops[2].Ahead)
}

func TestTripDetail_HeadwayTrip(t *testing.T) {
	ix := frequencyFixtureIndex(t)
	trip := mustTrip(t, ix, "T1")
	points := driveTrip(trip, reportDayStart, straightLegs(hms(9, 17, 0), hms(9, 20, 0), hms(9, 28, 10))(trip), nil)
	summary := TripSummary{ID: 5, VehicleID: "bus-1", RouteID: "R1", GtfsTripID: "T1", StartTime: reportDayStart.Add(hms(9, 17, 0)), Status: "completed"}

	d := newTripDetail(summary, points, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	assert.False(t, d.Timetable)
	assert.Equal(t, reportTripRow{}, d.Summary)
	assert.Contains(t, d.Note, "no timetable")
	assert.Equal(t, []tripStopRow{
		{Sequence: 1, Name: "Stop ST1", Passed: "09:20:06", Timed: true},
		{Sequence: 2, Name: "Stop ST2", Passed: "09:28:16", Timed: true},
		{Sequence: 3, Name: "Stop ST3", Passed: "09:29:44", Timed: true},
	}, d.Stops)

	body := renderTripDetailContent(t, d)
	assert.NotContains(t, body, "Scheduled")
	assert.NotContains(t, body, "Difference")
	assert.Contains(t, body, "09:28:16")
}

// TestTripDetail_NoRunNearby is T1, a weekday trip, driven on a Saturday: no
// run of it is near enough to judge it against.
func TestTripDetail_NoRunNearby(t *testing.T) {
	ix := reportFixtureIndex(t)
	trip := mustTrip(t, ix, "T1")
	saturday := reportDayStart.AddDate(0, 0, 3)
	points := driveTrip(trip, saturday, straightLegs(hms(7, 57, 0), hms(8, 0, 0), hms(8, 8, 10))(trip), nil)
	summary := TripSummary{ID: 6, VehicleID: "bus-1", RouteID: "R1", GtfsTripID: "T1", StartTime: saturday.Add(hms(7, 57, 0)), Status: "completed"}

	d := newTripDetail(summary, points, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	assert.False(t, d.Timetable)
	assert.Contains(t, d.Note, "No run of trip T1 is scheduled within 12 h")
	assert.Equal(t, "08:00:06", d.Stops[0].Passed)
	assert.Empty(t, d.Stops[0].Scheduled)
}

func TestTripDetail_RouteOnlyTrip(t *testing.T) {
	ix := reportFixtureIndex(t)
	trip, points := fixtureTrip(t, ix, 1)
	trip.GtfsTripID = ""

	d := newTripDetail(trip, points, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	assert.Equal(t, "1", d.Route.Name, "the route still comes from the schedule")
	assert.Equal(t, "#0077C0", d.Map.Color)
	assert.False(t, d.Timetable)
	assert.Empty(t, d.Stops)
	assert.Empty(t, d.Note)
	assert.Empty(t, d.Map.Stops)
	assert.Empty(t, d.Map.Shape)
	assert.Equal(t, len(points), d.Reports)
	assert.NotEmpty(t, d.CSV)

	body := renderTripDetailContent(t, d)
	assert.NotContains(t, body, `id="trip-stops"`, "map and gaps only")
	assert.Contains(t, body, `id="trip-gaps"`)
	assert.Contains(t, body, "None, route only")
}

func TestTripDetail_NoStopsToShow(t *testing.T) {
	ix := reportFixtureIndex(t)
	trip, points := fixtureTrip(t, ix, 1)

	d := newTripDetail(trip, points, nil, testReportSettings, 5*time.Minute, reportFixtureNow)
	assert.Equal(t, "UTC", d.Timezone)
	assert.Equal(t, "2026-09-02 14:57 UTC", d.Start)
	assert.Equal(t, "The GTFS schedule is not loaded, so the trip's stops can't be shown.", d.Note)
	assert.Empty(t, d.Stops)
	assert.Equal(t, defaultTrailColor, d.Map.Color)

	trip.GtfsTripID = "GONE"
	d = newTripDetail(trip, points, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	assert.Equal(t, "Trip GONE is not in the GTFS schedule now loaded, so its stops can't be shown.", d.Note)
	assert.Empty(t, d.Stops)
}

// TestTripDetail_ShapeFromStops is T4, which has no shape: as on the Reports
// page, the vehicle is not placed and every stop is unknown.
func TestTripDetail_ShapeFromStops(t *testing.T) {
	ix := reportFixtureIndex(t)
	trip := mustTrip(t, ix, "T4")
	require.True(t, trip.ShapeFromStops)
	points := driveTrip(trip, reportDayStart, stopLegs(hms(9, 58, 0), hms(10, 0, 0))(trip), nil)
	summary := TripSummary{ID: 7, VehicleID: "bus-1", RouteID: "R1", GtfsTripID: "T4", StartTime: reportDayStart.Add(hms(9, 58, 0)), Status: "completed"}

	d := newTripDetail(summary, points, ix, testReportSettings, 5*time.Minute, reportFixtureNow)
	assert.True(t, d.Timetable)
	assert.Contains(t, d.Note, "has no shape")
	for _, row := range d.Stops {
		assert.Empty(t, row.Passed, row.Name)
		assert.Equal(t, "unknown", row.Deviation.Tone, row.Name)
	}
	assert.Empty(t, d.Map.Shape, "a shape drawn through the stops is not the way the trip goes")
	assert.Len(t, d.Map.Stops, 2)
}

func TestTripGaps(t *testing.T) {
	start := time.Date(2026, 9, 2, 15, 0, 0, 0, time.UTC).Unix()
	points := []LocationPoint{
		{Timestamp: start},
		{Timestamp: start + 300}, // exactly the threshold: not a gap
		{Timestamp: start + 601}, // a second over it
		{Timestamp: start + 606},
	}
	gaps := tripGaps(points, 5*time.Minute, time.UTC)
	assert.Equal(t, []tripGap{{From: "15:05:00", To: "15:10:01", Length: "5 min", Index: 1}}, gaps)

	assert.Empty(t, tripGaps(points[:1], 5*time.Minute, time.UTC))
	assert.Empty(t, tripGaps(nil, 5*time.Minute, time.UTC))
}

// csvWindow is the from and to a CSV part's link asks the export for.
func csvWindow(t *testing.T, part tripCSVPart) (from, to int64) {
	t.Helper()
	u, err := url.Parse(part.URL)
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/admin/vehicles/bus-1/locations", u.Path)
	q := u.Query()
	assert.Equal(t, "csv", q.Get("format"))
	assert.Equal(t, strconv.Itoa(maxHistoryLimit), q.Get("limit"))
	from, err = strconv.ParseInt(q.Get("from"), 10, 64)
	require.NoError(t, err)
	to, err = strconv.ParseInt(q.Get("to"), 10, 64)
	require.NoError(t, err)
	return from, to
}

func TestTripCSVParts(t *testing.T) {
	start := time.Date(2026, 9, 2, 15, 0, 0, 0, time.UTC).Unix()
	every := func(n int, timestamp func(i int) int64) []LocationPoint {
		points := make([]LocationPoint, n)
		for i := range points {
			points[i].Timestamp = timestamp(i)
		}
		return points
	}

	t.Run("one part", func(t *testing.T) {
		parts := tripCSVParts("bus-1", every(3, func(i int) int64 { return start + int64(5*i) }), time.UTC)
		require.Len(t, parts, 1)
		from, to := csvWindow(t, parts[0])
		assert.Equal(t, start, from)
		assert.Equal(t, start+10, to)
		assert.Equal(t, "15:00:00 to 15:00:10", parts[0].Label)
	})

	t.Run("no reports", func(t *testing.T) {
		assert.Empty(t, tripCSVParts("bus-1", nil, time.UTC))
	})

	// Each window must hold every report once, and no more than one download
	// returns, however the reports fall across seconds.
	for _, tc := range []struct {
		name      string
		points    []LocationPoint
		wantParts int
	}{
		{"exactly one download", every(maxHistoryLimit, func(i int) int64 { return start + int64(i) }), 1},
		{"one over", every(maxHistoryLimit+1, func(i int) int64 { return start + int64(i) }), 2},
		{"several parts", every(2500, func(i int) int64 { return start + int64(5*i) }), 3},
		// Reports 995 to 1004 share a second. The first part ends before it,
		// at 995 reports, since a window that cut through it would push its
		// other reports into the next part and make that 1005.
		{"a second across the boundary", every(2*maxHistoryLimit, func(i int) int64 {
			switch {
			case i < 995:
				return start + int64(i)
			case i < 1005:
				return start + 995
			}
			return start + int64(i) - 9
		}), 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := tripCSVParts("bus-1", tc.points, time.UTC)
			require.Len(t, parts, tc.wantParts)
			seen := 0
			var prevTo int64
			for i, part := range parts {
				from, to := csvWindow(t, part)
				if i > 0 {
					assert.Equal(t, prevTo+1, from, "part %d starts the second after the last ends", i)
				}
				in := 0
				for _, p := range tc.points {
					if p.Timestamp >= from && p.Timestamp <= to {
						in++
					}
				}
				assert.LessOrEqual(t, in, maxHistoryLimit, "part %d fits one download", i)
				seen += in
				prevTo = to
			}
			assert.Equal(t, len(tc.points), seen, "every report in exactly one part")
		})
	}
}

// renderTripDetailContent renders the trip page's content for d, without the
// layout around it.
func renderTripDetailContent(t *testing.T, d tripDetail) string {
	t.Helper()
	ui := newTestAdminUI(t)
	w := httptest.NewRecorder()
	ui.renderAdmin(w, httptest.NewRequest(http.MethodGet, "/admin/trips/1", nil), http.StatusOK, "trip_detail.html", map[string]interface{}{
		"Title": "Trip", "Page": "trips", "Trip": d,
	})
	require.Equal(t, http.StatusOK, w.Code)
	return w.Body.String()
}

func TestTripDetailPage_CompletedTrip(t *testing.T) {
	r, _, ix := newFixtureReporter(t, reportFixtureNow)
	trip, points := fixtureTrip(t, ix, 1)
	end := trip.StartTime.Add(15 * time.Minute)
	trip.EndTime = &end
	mux := newTripDetailMux(t, &fakeTripTrailStore{trip: &trip, points: points}, r)

	body := getAdminPage(t, mux, "/admin/trips/1")
	assert.Contains(t, body, "Times in America/Los_Angeles.")
	assert.Contains(t, body, "Bus T1")
	assert.Contains(t, body, "Driver T1")
	assert.Contains(t, body, `style="background-color: #0077C0; color: #FFFFFF"`, "the route badge in its colours")
	assert.Contains(t, body, "2026-09-02 08:12 PDT")
	assert.Contains(t, body, "15m")
	assert.Contains(t, body, "08:08:16")
	assert.Contains(t, body, "3:16 late")
	assert.Contains(t, body, "No gap of over 5 min between reports.")
	assert.NotContains(t, body, `action="/admin/trips/1/end"`, "a finished trip can't be ended")
	assert.NotContains(t, body, "New reports are added")

	parts := tripCSVParts(trip.VehicleID, points, ix.Timezone())
	require.Len(t, parts, 1)
	assert.Contains(t, body, `href="`+html.EscapeString(parts[0].URL)+`"`)
	assert.Contains(t, body, "Download CSV")

	data := pageTripMap(t, body)
	assert.Equal(t, "/api/v1/admin/trips/1/locations", data.TrailURL)
	assert.Equal(t, "#0077C0", data.Color)
	assert.Equal(t, 300.0, data.GapSeconds)
	assert.Equal(t, "America/Los_Angeles", data.Timezone)
	assert.False(t, data.Active)
	assert.Len(t, data.Stops, 3)
	assert.Equal(t, "Stop ST1", data.Stops[0].Name)
}

func TestTripDetailPage_ActiveTrip(t *testing.T) {
	r, _, ix := newFixtureReporter(t, reportFixtureNow)
	trip, points := fixtureTrip(t, ix, 1)
	trip.Status = "active"
	mux := newTripDetailMux(t, &fakeTripTrailStore{trip: &trip, points: points}, r)

	body := getAdminPage(t, mux, "/admin/trips/1")
	assert.Contains(t, body, `action="/admin/trips/1/end"`, "End trip, through the Trips page's handler")
	assert.Contains(t, body, `onsubmit="return confirm('End this trip? Do this only when the driver phone cannot end it.')"`)
	assert.Contains(t, body, "so far")
	assert.Contains(t, body, "New reports are added every 10 seconds.")
	assert.True(t, pageTripMap(t, body).Active, "the script fetches the trail again")
}

func TestTripDetailPage_Gaps(t *testing.T) {
	r, _, ix := newFixtureReporter(t, reportFixtureNow)
	trip, points := fixtureTrip(t, ix, 1)
	// The phone is silent for six minutes from 07:58.
	points = withoutFixes(points, reportDayStart, hms(7, 58, 0), hms(8, 4, 10))
	mux := newTripDetailMux(t, &fakeTripTrailStore{trip: &trip, points: points}, r)

	body := getAdminPage(t, mux, "/admin/trips/1")
	assert.Contains(t, body, "07:58:00")
	assert.Contains(t, body, "08:04:10")
	assert.Contains(t, body, "6 min")
	assert.Regexp(t, `data-gap="\d+"`, body, "a button to show it on the map")
}

func TestTripsPage_LinksTheTripPage(t *testing.T) {
	ui := newTestAdminUI(t)
	start := time.Date(2026, 1, 2, 7, 0, 0, 0, time.UTC)
	ui.trips = &fakeTripLister{trips: []TripSummary{{ID: 42, VehicleID: "bus-1", VehicleLabel: "Bus One", StartTime: start, Status: "active"}}}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	assert.Contains(t, getAdminPage(t, mux, "/admin/trips"), `<a href="/admin/trips/42" class="text-sky-600 hover:text-sky-800">42</a>`)
}
