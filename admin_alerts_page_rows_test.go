package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

func TestAlertsHeadline(t *testing.T) {
	assert.Equal(t, "No alerts are open", alertsHeadline(0))
	assert.Equal(t, "1 alert is open", alertsHeadline(1))
	assert.Equal(t, "5 alerts are open", alertsHeadline(5))
}

// TestAlertsPage_Rows: each alert is a row card marked with its kind, which
// the chips count and filter by. Every kind is amber: red and blue pills are
// early and late, and an alert is neither.
func TestAlertsPage_Rows(t *testing.T) {
	now := time.Now()
	mux := newAlertsMux(t, twoAlerts(now))

	body := getAdminPage(t, mux, "/admin/alerts")

	assert.Equal(t, 2, strings.Count(body, `<article class="card alert-row"`))
	assert.Contains(t, body, `<article class="card alert-row" data-kind="quiet">`)
	assert.Contains(t, body, `<article class="card alert-row" data-kind="not_moving">`)
	assert.Contains(t, body, assetURL(t, "icons.svg")+"#signal_disconnected")
	assert.Contains(t, body, assetURL(t, "icons.svg")+"#stop_circle")
	assert.Contains(t, body, `<span class="pill pill-warn">Gone quiet</span>`)
	assert.Contains(t, body, `<span class="pill pill-warn">Not moving</span>`)
	for _, class := range []string{"pill-early", "pill-late", "pill-error"} {
		assert.NotContains(t, body, class)
	}

	// twoAlerts: bus-quiet last reported five minutes ago, in UTC with no
	// schedule loaded.
	assert.Contains(t, body, "No report for 5 min, open since "+now.Add(-5*time.Minute).UTC().Format("15:04")+"</span>")

	for _, chip := range []string{
		`data-filter="all" aria-pressed="true">All <span class="n">2</span>`,
		`data-filter="quiet" aria-pressed="false">Gone quiet <span class="n">1</span>`,
		`data-filter="not_moving" aria-pressed="false">Not moving <span class="n">1</span>`,
		`data-filter="off_route" aria-pressed="false">Off route <span class="n">0</span>`,
		`data-filter="not_started" aria-pressed="false">Not started <span class="n">0</span>`,
		`data-filter="bunched" aria-pressed="false">Bunched <span class="n">0</span>`,
	} {
		assert.Contains(t, body, chip)
	}
	assert.Contains(t, body, `<div class="alert-kinds js-only"`, "the chips need JavaScript; every row shows without it")
	assert.Contains(t, body, `<a class="btn btn-sm btn-tonal" href="/admin/trips/7">`)
	assert.Contains(t, body, `>Last position</a>`)
	assert.Contains(t, body, `<a class="btn btn-sm btn-outline" href="/admin/trips?page=1&amp;status=active&amp;vehicle_id=bus-quiet">Trip</a>`)
}

// TestAlertsPage_KindWithNoAlert: a chip can pick a kind with no open alert,
// Off route in twoAlerts, so the list carries a hidden line the script shows
// then. With no alert open at all the page says All clear instead.
func TestAlertsPage_KindWithNoAlert(t *testing.T) {
	const none = `<div class="card" id="alert-none" hidden><div class="empty"><div class="empty-title">No open alerts of this kind</div>`

	body := getAdminPage(t, newAlertsMux(t, twoAlerts(time.Now())), "/admin/alerts")
	assert.Contains(t, body, none)
	assert.Less(t, strings.Index(body, `id="alert-list"`), strings.Index(body, none), "the line comes after the rows")

	empty := getAdminPage(t, newAlertsMux(t, newAlertWatcher(&fakeTripLister{}, testAlertSettings)), "/admin/alerts")
	assert.NotContains(t, empty, `id="alert-none"`)
	assert.Contains(t, empty, `<div class="empty-title">All clear</div>`)
}

// TestNewAlertRow_RouteFromTheSchedule: with a schedule the row carries the
// route's badge and where the trip goes, and for a block's next trip those
// are the trip not started, not the one run.
func TestNewAlertRow_RouteFromTheSchedule(t *testing.T) {
	ix := reportFixtureIndex(t)
	since := time.Date(2026, 9, 2, 15, 4, 0, 0, time.UTC)
	trip := alertTrip("bus-7", since.Add(-time.Hour))
	trip.RouteID, trip.GtfsTripID = "R1", "T1"

	quiet := newAlertRow(alertEntry{Kind: alertQuiet, Since: since, DurationSeconds: 240, Trip: trip}, testAlertSettings, ix, ix.Timezone())
	assert.Equal(t, "Gone quiet", quiet.Title)
	assert.Equal(t, "signal_disconnected", quiet.Icon)
	assert.Equal(t, "No report for 4 min", quiet.Detail)
	assert.Equal(t, "08:04", quiet.Since, "on the agency's clock")
	assert.Equal(t, tripRoute{ID: "R1", Name: "1", LongName: "Straight", Color: "#0077C0", TextColor: "#FFFFFF"}, quiet.Route)
	assert.Equal(t, "Straight to North", quiet.RouteLine)

	next := &alertNextTrip{GtfsTripID: "RE", RouteID: "R1", BlockID: "B1"}
	late := newAlertRow(alertEntry{Kind: alertNotStarted, Since: since, DurationSeconds: 360, Trip: trip, NextTrip: next}, testAlertSettings, ix, ix.Timezone())
	assert.Equal(t, "Not started", late.Title)
	assert.Equal(t, "pending_actions", late.Icon)
	assert.Equal(t, "Next on block B1, due 6 min ago", late.Detail)
	assert.Equal(t, "RE", late.GtfsTripID)
	assert.Equal(t, "/admin/trips?page=1&vehicle_id=bus-7", late.TripURL)

	bare := newAlertRow(alertEntry{Kind: alertNotMoving, Since: since, DurationSeconds: 720, Trip: trip}, testAlertSettings, nil, time.UTC)
	assert.Equal(t, "stop_circle", bare.Icon)
	assert.Equal(t, tripRoute{ID: "R1", Name: "R1"}, bare.Route)
	assert.Empty(t, bare.RouteLine)
	assert.Equal(t, "15:04", bare.Since)
}

// TestAlertKinds_EveryKindHasAChip: a kind the watcher raises and the page
// has no chip, title or icon for would show as a row with an empty pill.
func TestAlertKinds_EveryKindHasAChip(t *testing.T) {
	ix := (*rider.Index)(nil)
	for _, kind := range []string{alertQuiet, alertNotMoving, alertOffRoute, alertNotStarted, alertBunched} {
		row := newAlertRow(alertEntry{Kind: kind, Trip: alertTrip("bus-1", alertT0), NextTrip: &alertNextTrip{}, Ahead: &alertAhead{}}, testAlertSettings, ix, time.UTC)
		require.NotEmpty(t, row.Title, kind)
		assert.NotEmpty(t, row.Icon, kind)
		assert.NotEmpty(t, row.Detail, kind)
	}
	assert.Len(t, alertKinds, 5)
}
