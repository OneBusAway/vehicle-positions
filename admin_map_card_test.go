package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdminMapCardReadsTheStopItIsGiven pins how the live map's card finds a
// vehicle's stop to the names the server writes: the stop fields and status of
// GET /api/v1/admin/vehicles/live, and the stop fields, timezone and
// from_frequencies of the trip answer it names the stop from. Each expected
// expression is built from the structs' JSON tags, so renaming a field on the
// server fails here rather than leaving the card's stop row saying "Not known"
// for every vehicle, or showing a frequency trip's pattern times as its run's.
// The card's Schedule row reads schedule_deviation_s the same way, and the
// on-time window from the attributes the map page writes (TestMapPageLiveMode).
func TestAdminMapCardReadsTheStopItIsGiven(t *testing.T) {
	h := newTestHandler(t, true)
	req := httptest.NewRequest(http.MethodGet, "/static/js/map.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	script := w.Body.String()

	tag := func(of any, field string) string {
		t.Helper()
		f, ok := reflect.TypeOf(of).FieldByName(field)
		require.True(t, ok, "%T has no field %s", of, field)
		return strings.Split(f.Tag.Get("json"), ",")[0]
	}
	live, stop, trip := liveVehicleEntry{}, catalogStop{}, catalogTripResponse{}
	for _, want := range []string{
		"s." + tag(stop, "ID") + " === v." + tag(live, "StopID"),
		"s." + tag(stop, "Sequence") + " === v." + tag(live, "StopSequence"),
		"v." + tag(live, "CurrentStatus") + ` === "` + gtfsrt.VehiclePosition_STOPPED_AT.String() + `"`,
		"s." + tag(stop, "ArrivalAt") + " : s." + tag(stop, "DepartureAt"),
		"t." + tag(trip, "Timezone"),
		"if (t." + tag(trip, "FromFrequencies") + `) return "";`,
		"schedule(v." + tag(live, "ScheduleDeviationS") + ")",
		"el.dataset.windowEarly",
		"el.dataset.windowLate",
	} {
		// Not assert.Contains: on failure it prints the whole script.
		assert.True(t, strings.Contains(script, want), "map.js is missing %s", want)
	}
}
