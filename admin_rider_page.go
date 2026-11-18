package main

import (
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// riderPageSource is what the Rider mode page reads from the rider service:
// its account of itself, the answer GET /api/v1/admin/rider/status gives, and
// what the riders of one trip add up to now, as
// GET /api/v1/rider/trips/{trip_id}/status works it out. *riderService
// implements it, and riderOff stands in for it when rider mode is off.
type riderPageSource interface {
	riderStatusProvider
	TripNow(key rider.TripKey) (inFeed bool, riders int)
}

// TripNow is handleTripStatus's answer for one trip at the service's clock:
// whether the riders' position for it is in the feed, and how many riders
// count towards it.
func (s *riderService) TripNow(key rider.TripKey) (inFeed bool, riders int) {
	now := s.now()
	return s.agg.TripStatus(key, now, s.coveredFunc(now))
}

// TripNow: with rider mode off, no rider counts towards any trip.
func (riderOff) TripNow(rider.TripKey) (bool, int) { return false, 0 }

// riderPageFeed is one trusted feed as the Rider mode page shows it.
type riderPageFeed struct {
	URL string
	// State is "working", "failing" (its last poll failed), or "waiting"
	// (it has not been polled yet).
	State       string
	LastSuccess string
	LastError   string
	Entities    int
}

// riderRideRow is one ride in the Rider mode page's list.
type riderRideRow struct {
	TripID string
	// StartDay is the service day of the trip, as "2 Sep 2026".
	StartDay string
	// DriverTrip is the one driver trip of this server running the same
	// scheduled trip, for an active ride; 0 when there is none, or more than
	// one and the page can't tell which vehicle the rider is on.
	DriverTrip   int64
	Route        tripRoute
	State        string
	Corroborated bool
	// Live is whether the ride is in progress. Riders and InFeed are the
	// trip's now, for a ride in progress only.
	Live          bool
	Riders        int
	InFeed        bool
	PointsMatched int
	PointsTotal   int
	// Started and Ended are times of day on the agency's clock; Ended is ""
	// for a ride in progress.
	Started   string
	Ended     string
	EndReason string
}

// riderPage renders the Rider mode page: what rider mode is doing, from the
// rider service's own status, and its rides a page at a time, from the store
// query the admin rides list uses. With rider mode off it says so and nothing
// else.
func (ui *adminUI) riderPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status, err := ui.rider.RiderStatus(ctx)
	if err != nil {
		slog.Error("rider page: rider status", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	data := map[string]interface{}{
		"Title":   "Rider mode",
		"Page":    "rider",
		"Enabled": status.Enabled,
	}
	if !status.Enabled {
		ui.renderAdmin(w, r, http.StatusOK, "rider.html", data)
		return
	}

	// With no status the list is every ride, which the store lists for "".
	query := r.URL.Query()
	rideStatus := query.Get("status")
	if rideStatus != "" && !riderRideStatuses[rideStatus] {
		http.Error(w, "status must be active or ended", http.StatusBadRequest)
		return
	}
	page := adminPageNumber(query)

	rides, err := ui.rides.ListRides(ctx, rideStatus, adminPageSize+1, adminPageOffset(page))
	if err != nil {
		slog.Error("rider page: list rides", "status", rideStatus, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	hasMore := len(rides) > adminPageSize
	if hasMore {
		rides = rides[:adminPageSize]
	}

	// An ended ride's trip may be a run from days ago, so only rides in
	// progress are matched with what is on the road now.
	driverTrips := map[string]int64{}
	if slices.ContainsFunc(rides, func(ride Ride) bool { return ride.Status == "active" }) {
		active, err := ui.activeTrips.ListActiveTripsByVehicle(ctx)
		if err != nil {
			slog.Error("rider page: list active trips by vehicle", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		driverTrips = soleDriverTrips(active)
	}

	ix := ui.reports.index()
	loc := time.UTC
	if ix != nil {
		loc = ix.Timezone()
	}
	rows := make([]riderRideRow, 0, len(rides))
	for _, ride := range rides {
		row := newRiderRideRow(ride, ix, loc)
		if row.Live {
			row.DriverTrip = driverTrips[ride.TripID]
			row.InFeed, row.Riders = ui.rider.TripNow(rider.TripKey{TripID: ride.TripID, StartDate: ride.StartDate})
		}
		rows = append(rows, row)
	}

	// GTFS is nil until the rider engine's first schedule load has landed.
	if status.GTFS != nil {
		data["Schedule"] = status.GTFS
		data["ScheduleLoaded"] = riderPageTime(status.GTFS.LoadedAt, loc)
	}
	data["Feeds"] = riderPageFeeds(status.TrustedFeeds, loc)
	data["Riders"] = status.Riders
	data["Rides"] = status.Rides
	data["Timezone"] = loc.String()
	data["Status"] = rideStatus
	data["Statuses"] = []statusLink{
		{Label: "All", URL: "/admin/rider", Current: rideStatus == ""},
		{Label: "In progress", URL: "/admin/rider?status=active", Current: rideStatus == "active"},
		{Label: "Ended", URL: "/admin/rider?status=ended", Current: rideStatus == "ended"},
	}
	data["List"] = rows
	data["Shown"] = newPagerCount(len(rows), "ride", "rides")
	data["PageNum"] = page
	data["HasMore"] = hasMore
	data["PrevURL"] = riderPageURL(rideStatus, page-1)
	data["NextURL"] = riderPageURL(rideStatus, page+1)
	ui.renderAdmin(w, r, http.StatusOK, "rider.html", data)
}

// newRiderRideRow is ride as the page lists it, its route named and coloured
// from ix when the schedule has it, and its times on loc.
func newRiderRideRow(ride Ride, ix *rider.Index, loc *time.Location) riderRideRow {
	row := riderRideRow{
		TripID:        ride.TripID,
		StartDay:      ride.StartDate,
		Route:         routeBadge(ix, ride.RouteID),
		State:         ride.State,
		Corroborated:  ride.Corroborated,
		Live:          ride.Status == "active",
		PointsMatched: ride.PointsMatched,
		PointsTotal:   ride.PointsTotal,
		Started:       ride.StartedAt.In(loc).Format("15:04"),
		EndReason:     strings.ReplaceAll(ride.EndReason, "_", " "),
	}
	// A start date the schedule's layout cannot read is shown as it is.
	if day, err := time.Parse(gtfsDateLayout, ride.StartDate); err == nil {
		row.StartDay = day.Format("2 Jan 2006")
	}
	if ride.EndedAt != nil {
		row.Ended = ride.EndedAt.In(loc).Format("15:04")
	}
	return row
}

// soleDriverTrips maps each scheduled trip a driver of this server is running
// to that driver trip, leaving out a scheduled trip run by more than one: a
// trip in frequencies.txt runs again and again, and a rider on it could be on
// any of its vehicles. A driver trip with only a route lands under "", which
// no ride has: a ride can't start without a trip in the schedule.
func soleDriverTrips(active map[string]ActiveTripInfo) map[string]int64 {
	byTrip := make(map[string]int64, len(active))
	shared := make(map[string]struct{})
	for _, trip := range active {
		if _, seen := byTrip[trip.GtfsTripID]; seen {
			shared[trip.GtfsTripID] = struct{}{}
		}
		byTrip[trip.GtfsTripID] = trip.TripID
	}
	for id := range shared {
		delete(byTrip, id)
	}
	return byTrip
}

// riderPageFeeds is the trusted feeds' health as the page shows it.
func riderPageFeeds(feeds []riderFeedStatus, loc *time.Location) []riderPageFeed {
	out := make([]riderPageFeed, 0, len(feeds))
	for _, f := range feeds {
		feed := riderPageFeed{
			URL:         f.URL,
			State:       "working",
			LastSuccess: riderPageTime(f.LastSuccess, loc),
			LastError:   f.LastError,
			Entities:    f.Entities,
		}
		switch {
		case f.LastError != "":
			feed.State = "failing"
		case f.LastSuccess == "":
			feed.State = "waiting"
		}
		out = append(out, feed)
	}
	return out
}

// riderPageTime is one of the status endpoint's RFC 3339 times on the page's
// clock, with how long ago it was; "" for the empty string the endpoint gives
// for a time that has not happened.
func riderPageTime(rfc3339 string, loc *time.Location) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return ""
	}
	return formatTripTime(t, loc) + ", " + humanizeAge(t)
}

// riderPageURL is a /admin/rider link with the status filter kept and page
// set, for the prev/next links.
func riderPageURL(status string, page int) string {
	v := url.Values{}
	if status != "" {
		v.Set("status", status)
	}
	v.Set("page", strconv.Itoa(page))
	return "/admin/rider?" + v.Encode()
}
