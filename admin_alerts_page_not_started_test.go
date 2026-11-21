package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// TestAlertsPage_NotStarted shows a not-started alert as the Alerts page and
// the dashboard's badge see it. The page works its alerts out at the real
// time, so the test builds block B1 around now: T1 left forty minutes ago and
// van-7 ran it, and T7 was due to leave six minutes ago and is due to arrive
// in four.
func TestAlertsPage_NotStarted(t *testing.T) {
	now := time.Now()
	// A service day four hours or more under way, so that every time below
	// falls after its start; GTFS counts the hours past midnight on from 24.
	serviceDate := now.In(feedZone).Add(-4 * time.Hour).Format("20060102")
	dayStart, err := rider.ServiceDayStart(serviceDate, feedZone)
	require.NoError(t, err)
	clock := func(fromNow time.Duration) string {
		d := now.Sub(dayStart).Truncate(time.Second) + fromNow
		return fmt.Sprintf("%02d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
	}
	ix := fixtureIndexEdited(t, func(name string, body []byte) []byte {
		switch name {
		case "calendar.txt":
			// SAT runs no day: its T2 is on block B1 at 09:00, so on a Saturday
			// morning it would come between T1 and T7 and be the trip not started.
			cal := strings.Replace(string(body), "WEEKDAY,1,1,1,1,1,0,0", "WEEKDAY,1,1,1,1,1,1,1", 1)
			return []byte(strings.Replace(cal, "SAT,0,0,0,0,0,1,0", "SAT,0,0,0,0,0,0,0", 1))
		case "trips.txt":
			return blockEdit(name, body)
		case "stop_times.txt":
			var kept []string
			for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
				if !strings.HasPrefix(line, "T1,") {
					kept = append(kept, line)
				}
			}
			for _, stop := range []struct {
				trip            string
				leaves, arrives time.Duration
			}{{"T1", -40 * time.Minute, -30 * time.Minute}, {"T7", -6 * time.Minute, 4 * time.Minute}} {
				kept = append(kept,
					fmt.Sprintf("%s,%s,%s,ST1,1,0.000000", stop.trip, clock(stop.leaves), clock(stop.leaves)),
					fmt.Sprintf("%s,%s,%s,ST3,2,1001.000000", stop.trip, clock(stop.arrives), clock(stop.arrives)))
			}
			return []byte(strings.Join(kept, "\n") + "\n")
		}
		return body
	})
	run := blockRun(7, "van-7", "T1", now.Add(-39*time.Minute), now.Add(-29*time.Minute))
	store := &blockTripsStore{recent: []TripSummary{run}}
	w := newAlertWatcher(store, notStartedSettings)
	w.watchRoutes(func() *rider.Index { return ix })
	w.watchBlocks(store)
	mux := newAlertsMux(t, w)

	body := getAdminPage(t, mux, "/admin/alerts")

	assert.Contains(t, body, "next trip has not started over 5 min after it was due")
	assert.Contains(t, body, "Not started")
	assert.Contains(t, body, "Next on block B1, due 6 min ago")
	assert.Contains(t, body, "Van 7")
	assert.Contains(t, body, "Achieng")
	assert.Contains(t, body, "Achieng, trip T7</span>", "the trip not started, not the one run")
	assert.Contains(t, body, `href="/admin/trips/7"`, "the last trip's page, whose map starts where the vehicle last reported")
	assert.Contains(t, body, `href="/admin/trips?page=1&amp;vehicle_id=van-7"`, "the vehicle's trips, since none is running")
	badge := alertBadge.FindStringSubmatch(body)
	require.NotNil(t, badge)
	assert.Equal(t, "1", badge[1])

	dashboard := alertBadge.FindStringSubmatch(getAdminPage(t, mux, "/admin/dashboard"))
	require.NotNil(t, dashboard, "the dashboard's sidebar counts it too")
	assert.Equal(t, "1", dashboard[1])
}

// TestAlertsPage_EmptyMentionsBlocks: the empty page says the blocks are fine
// too.
func TestAlertsPage_EmptyMentionsBlocks(t *testing.T) {
	mux := newAlertsMux(t, newAlertWatcher(&fakeTripLister{}, notStartedSettings))

	body := getAdminPage(t, mux, "/admin/alerts")

	assert.Contains(t, body, "next trip is late to start.")
}
