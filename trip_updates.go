package main

import (
	"net/http"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"google.golang.org/protobuf/proto"
)

// handleGetTripUpdates serves the GTFS-RT TripUpdates feed: the delay this
// server measured for each driver vehicle, so a OneBusAway server, which
// builds its vehicles from trip updates, shows them (proposal #491). It reads
// the same tracker as the positions feed, with the same staleness, and is
// mounted behind the same feed auth.
func handleGetTripUpdates(tracker *Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeFeed(w, r, buildTripUpdates(tracker.ActiveVehicles()))
	}
}

// buildTripUpdates is one TripUpdate per vehicle whose delay was measured
// (measuredTrip). A OneBusAway server reads a TripUpdate without a delay as
// exactly on time and predicts every later stop from it, so a vehicle whose
// delay is not known is left out, not published as on time.
//
// Rider-derived trips are not passed in: their positions are already an
// estimate from riders' reports, and a delay worked out from them would be a
// guess on a guess.
func buildTripUpdates(vehicles []*VehicleState) *gtfsrt.FeedMessage {
	clock := newFeedClock(time.Now())

	var entities []*gtfsrt.FeedEntity
	for _, v := range vehicles {
		if !measuredTrip(v) {
			continue
		}
		ts, ok := clock.vehicleTimestamp(v)
		if !ok {
			continue
		}
		// In whole seconds, rounded as the admin live answer rounds it.
		delay := proto.Int32(int32(v.ScheduleDeviation.Round(time.Second) / time.Second))
		entities = append(entities, &gtfsrt.FeedEntity{
			Id: proto.String(v.VehicleID),
			TripUpdate: &gtfsrt.TripUpdate{
				Trip:      driverTrip(v),
				Vehicle:   vehicleDescriptor(v),
				Timestamp: proto.Uint64(ts),
				Delay:     delay,
				// The validator requires one stop_time_update (E041). It is
				// the stop the positions feed names, the one the vehicle is at
				// or heading to, and carries the same delay, since a stop's
				// delay takes precedence over the trip's. SCHEDULED is the
				// default, set so the validator does not warn W009.
				StopTimeUpdate: []*gtfsrt.TripUpdate_StopTimeUpdate{{
					StopSequence:         proto.Uint32(uint32(v.StopSequence)),
					StopId:               proto.String(v.StopID),
					Arrival:              &gtfsrt.TripUpdate_StopTimeEvent{Delay: delay},
					ScheduleRelationship: gtfsrt.TripUpdate_StopTimeUpdate_SCHEDULED.Enum(),
				}},
			},
		})
	}
	return clock.message(entities)
}

// measuredTrip reports whether v's delay against its trip's timetable is
// known, and so whether the trip updates feed publishes it: it needs a
// measured ScheduleDeviation, and with it
//   - a trip_id: a route-only report names no trip to be late on;
//   - a trip run by timetable: a trip in frequencies.txt has stop times that
//     are only the pattern of its runs, so no timetable to be late against;
//   - a stop to carry the delay in the stop_time_update the validator
//     requires.
//
// The tracker already leaves ScheduleDeviation nil without each of these.
// They are checked here as well because they are the feed's own reasons, which
// a later change to how deviations are measured, for the admin map say, must
// not undo.
func measuredTrip(v *VehicleState) bool {
	return v.ScheduleDeviation != nil && v.TripID != "" && !v.FrequencyBased && v.StopID != ""
}
