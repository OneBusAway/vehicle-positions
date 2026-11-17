package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func TestAlertsPage_OffRoute(t *testing.T) {
	now := time.Now()
	trips := &fakeTripLister{}
	w := newAlertWatcher(trips, offRouteSettings)
	w.watchingSince = now.Add(-time.Hour)
	ix := plainFixtureIndex(t)
	w.watchRoutes(func() *rider.Index { return ix })
	trip := alertTrip("bus-7", now.Add(-time.Hour))
	w.report(onRun(routeFix("T1", alongT1(10, 3), nil), trip.StartTime), now.Add(-7*time.Minute))
	w.report(onRun(routeFix("T1", alongT1(100, 200), nil), trip.StartTime), now.Add(-6*time.Minute))
	w.report(onRun(routeFix("T1", alongT1(400, 200), nil), trip.StartTime), now)
	trip.VehicleLabel = "Van 7"
	trips.trips = []TripSummary{trip}
	mux := newAlertsMux(t, w)

	body := getAdminPage(t, mux, "/admin/alerts")

	assert.Contains(t, body, "or have been more than 60 m from their route for over 5 min")
	assert.Contains(t, body, "Off route")
	assert.Contains(t, body, "Over 60 m from its route for 6 min")
	assert.Contains(t, body, "Van 7")
	assert.Contains(t, body, `href="/admin/trips/7"`)
	assert.Contains(t, body, `href="/admin/trips?page=1&amp;status=active&amp;vehicle_id=bus-7"`)
	badge := alertBadge.FindStringSubmatch(body)
	require.NotNil(t, badge)
	assert.Equal(t, "1", badge[1])

	dashboard := alertBadge.FindStringSubmatch(getAdminPage(t, mux, "/admin/dashboard"))
	require.NotNil(t, dashboard, "the dashboard's sidebar counts it too")
	assert.Equal(t, "1", dashboard[1])
}
