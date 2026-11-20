package main

import (
	"cmp"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// alertsPageRefresh is how often, in seconds, the Alerts page reloads itself.
// It is sent as a Refresh header, so the page stays current without
// JavaScript.
const alertsPageRefresh = "30"

// alertRow is one alert on the Alerts page.
type alertRow struct {
	// Kind is the alert's kind as the API names it, which the page's chips
	// show and hide rows by; Title is the kind in words, and Icon its symbol
	// in web/static/icons.svg.
	Kind   string
	Title  string
	Icon   string
	Detail string
	// Since is when the alert began, on the page's clock.
	Since        string
	VehicleID    string
	VehicleLabel string
	DriverName   string
	Route        tripRoute
	// RouteLine names where the trip goes, as on the trip page; empty when
	// the schedule does not have the trip.
	RouteLine  string
	GtfsTripID string
	// MapURL opens the trip's page, whose map starts on the vehicle's last
	// report. The live map would not do: it drops a vehicle
	// STALENESS_THRESHOLD after its last report.
	MapURL string
	// TripURL opens the Trips page on this vehicle's active trip, which is
	// where the trip can be ended.
	TripURL string
}

// alertKindCount is one of the Alerts page's chips: a kind, or "all", and how
// many alerts of it are open.
type alertKindCount struct {
	Kind  string
	Label string
	Count int
}

// clearedAlertRow is one cleared alert on the Alerts page.
type clearedAlertRow struct {
	// Title is the kind in words.
	Title        string
	VehicleID    string
	VehicleLabel string
	// Route is the trip not started for a not-started alert, the trip
	// running for the others, and GtfsTripID that trip.
	Route      tripRoute
	GtfsTripID string
	// TripID opens the trip running, or for a not-started alert the
	// vehicle's last.
	TripID int64
	// Raised and Cleared are on the page's clock, RaisedDay the day it was
	// raised on, and Lasted is from one to the other.
	Raised    string
	RaisedDay string
	Cleared   string
	Lasted    string
}

// alertKinds are the kinds in the order the chips show them, with each kind
// in words and its symbol.
var alertKinds = []struct{ kind, title, icon string }{
	{alertQuiet, "Gone quiet", "signal_disconnected"},
	{alertNotMoving, "Not moving", "stop_circle"},
	{alertOffRoute, "Off route", "wrong_location"},
	{alertNotStarted, "Not started", "pending_actions"},
}

// newAlertRow is a's row, with the off-route distance from settings, the
// route's badge and the trip's headsign from the schedule ix (nil when none
// is loaded), and its time on the clock loc.
func newAlertRow(a alertEntry, settings alertSettings, ix *rider.Index, loc *time.Location) alertRow {
	duration := humanizeDuration(time.Duration(a.DurationSeconds) * time.Second)
	row := alertRow{
		Kind:         a.Kind,
		Since:        a.Since.In(loc).Format("15:04"),
		VehicleID:    a.Trip.VehicleID,
		VehicleLabel: a.Trip.VehicleLabel,
		DriverName:   a.Trip.DriverName,
		GtfsTripID:   a.Trip.GtfsTripID,
		MapURL:       "/admin/trips/" + strconv.FormatInt(a.Trip.ID, 10),
		TripURL:      tripsPageURL("active", a.Trip.VehicleID, "", 1),
	}
	routeID := a.Trip.RouteID
	switch a.Kind {
	case alertQuiet:
		row.Detail = "No report for " + duration
	case alertOffRoute:
		row.Detail = fmt.Sprintf("Over %.0f m from its route for %s", settings.offRouteDistance, duration)
	case alertNotStarted:
		// The vehicle and driver are the last trip's, the route and trip the
		// one not started. The vehicle has no trip running for Trip to open,
		// so it opens all of the vehicle's trips.
		row.Detail = fmt.Sprintf("Next on block %s, due %s ago", a.NextTrip.BlockID, duration)
		routeID = a.NextTrip.RouteID
		row.GtfsTripID = a.NextTrip.GtfsTripID
		row.TripURL = tripsPageURL("", a.Trip.VehicleID, "", 1)
	default:
		row.Detail = fmt.Sprintf("Under %.0f m in %s", stoppedRadius, duration)
	}
	for _, k := range alertKinds {
		if k.kind == a.Kind {
			row.Title, row.Icon = k.title, k.icon
		}
	}
	row.Route = routeBadge(ix, routeID)
	row.RouteLine = row.Route.LongName
	if ix != nil {
		if info, ok := ix.Trip(row.GtfsTripID); ok && info.Headsign != "" {
			row.RouteLine = "To " + info.Headsign
			if row.Route.LongName != "" {
				row.RouteLine = row.Route.LongName + " to " + info.Headsign
			}
		}
	}
	return row
}

// newClearedAlertRow is a's row, with the route's badge from the schedule ix
// (nil when none is loaded) and its times on the clock loc, as of now.
func newClearedAlertRow(a clearedAlert, ix *rider.Index, loc *time.Location, now time.Time) clearedAlertRow {
	row := clearedAlertRow{
		VehicleID:    a.VehicleID,
		VehicleLabel: cmp.Or(a.VehicleLabel, a.VehicleID),
		Route:        routeBadge(ix, a.RouteID),
		GtfsTripID:   a.GtfsTripID,
		TripID:       a.TripID,
		Raised:       a.RaisedAt.In(loc).Format("15:04"),
		RaisedDay:    formatDay(a.RaisedAt, loc, now),
		Cleared:      a.ClearedAt.In(loc).Format("15:04"),
		Lasted:       formatMinutes(a.ClearedAt.Sub(a.RaisedAt)),
	}
	row.Title, _ = alertKindTitle(a.Kind)
	return row
}

// alertKindTitle is kind in words, and false when there is no such kind.
func alertKindTitle(kind string) (string, bool) {
	for _, k := range alertKinds {
		if k.kind == kind {
			return k.title, true
		}
	}
	return "", false
}

// clearedAlertsURL is an /admin/alerts link that shows the cleared alerts of
// kind, "" for every kind, from page, at the list.
func clearedAlertsURL(kind string, page int) string {
	v := url.Values{}
	if kind != "" {
		v.Set("kind", kind)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if len(v) == 0 {
		return "/admin/alerts#cleared"
	}
	return "/admin/alerts?" + v.Encode() + "#cleared"
}

// alertsHeadline is the Alerts page's headline, from the count of open alerts.
func alertsHeadline(open int) string {
	switch open {
	case 0:
		return "No alerts are open"
	case 1:
		return "1 alert is open"
	default:
		return strconv.Itoa(open) + " alerts are open"
	}
}

// alertsPage renders the current alerts, longest-running first, and below
// them a page of the cleared ones, newest first, of every kind or of the one
// ?kind= names.
func (ui *adminUI) alertsPage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	kind := query.Get("kind")
	if _, ok := alertKindTitle(kind); kind != "" && !ok {
		http.Error(w, "kind must be quiet, not_moving, off_route or not_started", http.StatusBadRequest)
		return
	}
	page := adminPageNumber(query)

	// Set before the list, so a page left open keeps reloading through a
	// database outage and recovers when it ends.
	w.Header().Set("Refresh", alertsPageRefresh)
	now := time.Now()
	alerts, err := ui.alerts.list(r.Context(), now)
	if err != nil {
		slog.Error("alerts: list alerts", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// A page of rows at a time, read newest first through an index on when
	// each cleared, so the page costs the same however long the history.
	cleared, err := ui.clearedAlerts.ListClearedAlerts(r.Context(), kind, adminPageSize+1, adminPageOffset(page))
	if err != nil {
		slog.Error("alerts: list cleared alerts", "kind", kind, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	hasMore := len(cleared) > adminPageSize
	if hasMore {
		cleared = cleared[:adminPageSize]
	}

	ix := ui.reports.index()
	loc := pageLocation(ix)
	rows := make([]alertRow, 0, len(alerts))
	byKind := make(map[string]int, len(alertKinds))
	for _, a := range alerts {
		rows = append(rows, newAlertRow(a, ui.alerts.settings, ix, loc))
		byKind[a.Kind]++
	}
	kinds := make([]alertKindCount, 0, len(alertKinds)+1)
	kinds = append(kinds, alertKindCount{Kind: "all", Label: "All", Count: len(rows)})
	for _, k := range alertKinds {
		kinds = append(kinds, alertKindCount{Kind: k.kind, Label: k.title, Count: byKind[k.kind]})
	}

	clearedRows := make([]clearedAlertRow, 0, len(cleared))
	for _, a := range cleared {
		clearedRows = append(clearedRows, newClearedAlertRow(a, ix, loc, now))
	}
	clearedKinds := make([]statusLink, 0, len(alertKinds)+1)
	clearedKinds = append(clearedKinds, statusLink{Label: "All", URL: clearedAlertsURL("", 1), Current: kind == ""})
	for _, k := range alertKinds {
		clearedKinds = append(clearedKinds, statusLink{Label: k.title, URL: clearedAlertsURL(k.kind, 1), Current: kind == k.kind})
	}

	ui.renderAdminWithAlerts(w, r, http.StatusOK, "alerts.html", map[string]interface{}{
		"Title":            "Alerts",
		"Page":             "alerts",
		"Headline":         alertsHeadline(len(rows)),
		"Alerts":           rows,
		"Kinds":            kinds,
		"Cleared":          clearedRows,
		"ClearedKind":      kind,
		"ClearedKinds":     clearedKinds,
		"Timezone":         loc.String(),
		"Shown":            newPagerCount(len(clearedRows), "cleared alert", "cleared alerts"),
		"PageNum":          page,
		"HasMore":          hasMore,
		"PrevURL":          clearedAlertsURL(kind, page-1),
		"NextURL":          clearedAlertsURL(kind, page+1),
		"QuietAfter":       humanizeDuration(ui.alerts.settings.quietAfter),
		"StoppedAfter":     humanizeDuration(ui.alerts.settings.stoppedAfter),
		"StoppedRadius":    fmt.Sprintf("%.0f m", stoppedRadius),
		"OffRouteDistance": fmt.Sprintf("%.0f m", ui.alerts.settings.offRouteDistance),
		"OffRouteAfter":    humanizeDuration(ui.alerts.settings.offRouteAfter),
		"NotStartedAfter":  humanizeDuration(ui.alerts.settings.notStartedAfter),
	}, alerts)
}
