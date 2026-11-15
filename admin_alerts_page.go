package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// alertsPageRefresh is how often, in seconds, the Alerts page reloads itself.
// It is sent as a Refresh header, so the page stays current without
// JavaScript.
const alertsPageRefresh = "30"

// alertRow is one alert on the Alerts page.
type alertRow struct {
	Quiet        bool
	Kind         string
	Detail       string
	VehicleID    string
	VehicleLabel string
	DriverName   string
	RouteID      string
	GtfsTripID   string
	// MapURL opens the trip's trail, whose end marker is where the vehicle
	// last reported. The live map would not do: it drops a vehicle
	// STALENESS_THRESHOLD after its last report.
	MapURL string
	// TripURL opens the Trips page on this vehicle's active trip, which is
	// where the trip can be ended.
	TripURL string
}

// newAlertRow is a's row, with the off-route distance from settings.
func newAlertRow(a alertEntry, settings alertSettings) alertRow {
	duration := humanizeDuration(time.Duration(a.DurationSeconds) * time.Second)
	row := alertRow{
		Quiet:        a.Kind == alertQuiet,
		VehicleID:    a.Trip.VehicleID,
		VehicleLabel: a.Trip.VehicleLabel,
		DriverName:   a.Trip.DriverName,
		RouteID:      a.Trip.RouteID,
		GtfsTripID:   a.Trip.GtfsTripID,
		MapURL:       "/admin/map?trip_id=" + strconv.FormatInt(a.Trip.ID, 10),
		TripURL:      tripsPageURL("active", a.Trip.VehicleID, "", 1),
	}
	switch a.Kind {
	case alertQuiet:
		row.Kind = "Gone quiet"
		row.Detail = "No report for " + duration
	case alertOffRoute:
		row.Kind = "Off route"
		row.Detail = fmt.Sprintf("Over %.0f m from its route for %s", settings.offRouteDistance, duration)
	default:
		row.Kind = "Not moving"
		row.Detail = fmt.Sprintf("Under %.0f m in %s", stoppedRadius, duration)
	}
	return row
}

// alertsPage renders the current alerts, longest-running first.
func (ui *adminUI) alertsPage(w http.ResponseWriter, r *http.Request) {
	// Set before the list, so a page left open keeps reloading through a
	// database outage and recovers when it ends.
	w.Header().Set("Refresh", alertsPageRefresh)
	alerts, err := ui.alerts.list(r.Context(), time.Now())
	if err != nil {
		slog.Error("alerts: list alerts", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	rows := make([]alertRow, 0, len(alerts))
	for _, a := range alerts {
		rows = append(rows, newAlertRow(a, ui.alerts.settings))
	}

	ui.renderAdmin(w, r, http.StatusOK, "alerts.html", map[string]interface{}{
		"Title":            "Alerts",
		"Page":             "alerts",
		"Alerts":           rows,
		"AlertCount":       len(rows),
		"QuietAfter":       humanizeDuration(ui.alerts.settings.quietAfter),
		"StoppedAfter":     humanizeDuration(ui.alerts.settings.stoppedAfter),
		"StoppedRadius":    fmt.Sprintf("%.0f m", stoppedRadius),
		"OffRouteDistance": fmt.Sprintf("%.0f m", ui.alerts.settings.offRouteDistance),
		"OffRouteAfter":    humanizeDuration(ui.alerts.settings.offRouteAfter),
	})
}

// alertCount is the number on the sidebar's Alerts badge. A badge is not worth
// failing a page over, so an error is logged and the badge left off.
func (ui *adminUI) alertCount(ctx context.Context) int {
	alerts, err := ui.alerts.list(ctx, time.Now())
	if err != nil {
		slog.Error("sidebar: list alerts", "error", err)
		return 0
	}
	return len(alerts)
}
