package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// bunchClock is 07:20 on the report fixture's day, while route 5's GN runs
// every 10 minutes (gapFixtureIndex): its runs reach ST2, 500 m along S1 and
// five minutes after ST1, every 10 minutes from 07:05 to 08:05. Between ST1
// and ST2 its timetable gives 0.6 s a metre, so a vehicle 125 m behind
// another is 75 s behind it.
var bunchClock = reportDayStart.Add(hms(7, 20, 0))

// liveCar is one vehicle of a bunching test: bus-<name>, "Bus <name>", on GTFS
// trip trip, along metres up its shape, its fix taken fix after the test's
// clock and heard by the server heard after it. unplaced is a latest report
// that tripLocator did not place, off the route, so it named no stop.
// startDate is the run's start_date, the report fixture's day unless
// noStartDate, when the run is dated by its trip's start, startedAt after
// the report fixture's day began.
type liveCar struct {
	name        string
	trip        string
	along       float64
	fix         time.Duration
	heard       time.Duration
	unplaced    bool
	startDate   string
	noStartDate bool
	startedAt   time.Duration
}

// liveCars are cars as the active trips and the tracker have them, each
// trip numbered from 1 in order. A placed car names the stop tripLocator
// would: the first at or past along less stoppedAtDistance. Fix times count
// from fixClock and the server's from heardClock.
func liveCars(t *testing.T, ix *rider.Index, fixClock, heardClock time.Time, cars ...liveCar) ([]TripSummary, []*VehicleState) {
	t.Helper()
	trips := make([]TripSummary, 0, len(cars))
	states := make([]*VehicleState, 0, len(cars))
	for i, c := range cars {
		info := mustTrip(t, ix, c.trip)
		along := c.along
		v := &VehicleState{
			VehicleID: "bus-" + c.name, VehicleLabel: "Bus " + c.name, TripID: c.trip, RouteID: info.RouteID,
			StartDate: cmpOr(c.startDate, "20260902"), Timestamp: fixClock.Add(c.fix).Unix(),
			UpdatedAt: heardClock.Add(c.heard), alongShape: &along,
		}
		if c.noStartDate {
			v.StartDate = ""
		}
		if stop, ok := rider.NextStop(info, along-stoppedAtDistance); ok && !c.unplaced {
			v.StopID, v.StopSequence = stop.StopID, stop.Sequence
		}
		states = append(states, v)
		trips = append(trips, TripSummary{
			ID: int64(i + 1), VehicleID: v.VehicleID, VehicleLabel: v.VehicleLabel, RouteID: info.RouteID,
			GtfsTripID: c.trip, StartTime: reportDayStart.Add(c.startedAt), Status: "active",
		})
	}
	return trips, states
}

func cmpOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// bunchedFound is what the bunched check found of one vehicle, in whole
// seconds.
type bunchedFound struct {
	ahead     string
	spacing   int64
	scheduled int64
}

func foundOf(found map[string]vehicleSpacing) map[string]bunchedFound {
	out := make(map[string]bunchedFound, len(found))
	for id, s := range found {
		out[id] = bunchedFound{s.ahead.VehicleID, int64(s.spacing / time.Second), int64(s.scheduled / time.Second)}
	}
	return out
}

// shortTurnFixtureIndex is the report fixture with route 8 north along S1,
// both trips every 10 and 30 minutes from 07:00 to 08:00 from frequencies.txt
// so that ST2 is served every 7.5 minutes then: LN through ST1, ST2 and ST3
// as GN runs, and SH, which turns short at STX, 600 m along, a minute after
// ST2.
func shortTurnFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	zip := reportFixtureZip(t, func(name string, body []byte) []byte {
		switch name {
		case "routes.txt":
			return append(body, "R8,A,8,Short turns,3,8,,\n"...)
		case "stops.txt":
			return append(body, "STX,Stop STX,47.605396,-122.330000\n"...)
		case "trips.txt":
			return append(body, "R8,WEEKDAY,LN,S1,North,0\nR8,WEEKDAY,SH,S1,North,0\n"...)
		case "stop_times.txt":
			return append(body, strings.Join([]string{
				"LN,06:00:00,06:00:00,ST1,1,0.000000",
				"LN,06:05:00,06:05:00,ST2,2,500.000000",
				"LN,06:10:00,06:10:00,ST3,3,1001.000000",
				"SH,06:00:00,06:00:00,ST1,1,0.000000",
				"SH,06:05:00,06:05:00,ST2,2,500.000000",
				"SH,06:06:00,06:06:00,STX,3,600.000000",
			}, "\n")+"\n"...)
		}
		return body
	}, fixtureMember{"frequencies.txt", "trip_id,start_time,end_time,headway_secs,exact_times\nLN,07:00:00,08:00:00,600,0\nSH,07:00:00,08:00:00,1800,0\n"})
	ix, err := rider.IndexFromBytes(zip, "edited", catalogNow)
	require.NoError(t, err)
	return ix
}

// twoShapesFixtureIndex is the report fixture with route 9 north through
// ST1, ST2 and ST3 every 10 minutes from 07:00 to 08:00, NA along S1 and NB
// along S3, so ST2 is served every 5 minutes then.
func twoShapesFixtureIndex(t *testing.T) *rider.Index {
	t.Helper()
	zip := reportFixtureZip(t, func(name string, body []byte) []byte {
		switch name {
		case "routes.txt":
			return append(body, "R9,A,9,Two shapes,3,9,,\n"...)
		case "trips.txt":
			return append(body, "R9,WEEKDAY,NA,S1,North,0\nR9,WEEKDAY,NB,S3,North,0\n"...)
		case "stop_times.txt":
			lines := make([]string, 0, 6)
			for _, trip := range []string{"NA", "NB"} {
				lines = append(lines,
					trip+",06:00:00,06:00:00,ST1,1,0.000000",
					trip+",06:05:00,06:05:00,ST2,2,500.000000",
					trip+",06:10:00,06:10:00,ST3,3,1001.000000")
			}
			return append(body, strings.Join(lines, "\n")+"\n"...)
		}
		return body
	}, fixtureMember{"frequencies.txt", "trip_id,start_time,end_time,headway_secs,exact_times\nNA,07:00:00,08:00:00,600,0\nNB,07:00:00,08:00:00,600,0\n"})
	ix, err := rider.IndexFromBytes(zip, "edited", catalogNow)
	require.NoError(t, err)
	return ix
}

func TestStopSchedules_Bunched(t *testing.T) {
	gapIx, timetableIx, shortIx, twoShapesIx := gapFixtureIndex(t), timetableFixtureIndex(t), shortTurnFixtureIndex(t), twoShapesFixtureIndex(t)
	quiet := -(defaultAlertQuietAfter + time.Second)
	evening := reportDayStart.Add(hms(19, 20, 0))

	tests := []struct {
		name      string
		timetable bool
		shortTurn bool
		twoShapes bool
		clock     time.Time
		cars      []liveCar
		want      map[string]bunchedFound
	}{
		{
			name: "75 s behind on a route every 10 minutes",
			cars: []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 250}},
			want: map[string]bunchedFound{"bus-F": {"bus-L", 75, 600}},
		},
		{
			name: "149 s behind, just under 25%",
			cars: []liveCar{{name: "F", trip: "GN", along: 125, fix: -time.Second}, {name: "L", trip: "GN", along: 375}},
			want: map[string]bunchedFound{"bus-F": {"bus-L", 149, 600}},
		},
		{
			name: "150 s behind, exactly 25%",
			cars: []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 375}},
			want: map[string]bunchedFound{},
		},
		{
			name: "the vehicle ahead reported later, so it is further on than its place",
			cars: []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 375, fix: 30 * time.Second}},
			want: map[string]bunchedFound{"bus-F": {"bus-L", 120, 600}},
		},
		{
			name: "the vehicle behind reported later, so it is nearer than its place",
			cars: []liveCar{{name: "F", trip: "GN", along: 125, fix: 80 * time.Second}, {name: "L", trip: "GN", along: 250}},
			want: map[string]bunchedFound{},
		},
		{
			name: "the timetable gives no running time between two places the same",
			cars: []liveCar{{name: "F", trip: "GN", along: 250}, {name: "L", trip: "GN", along: 250}},
			want: map[string]bunchedFound{},
		},
		{
			name: "far apart",
			cars: []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 900}},
			want: map[string]bunchedFound{},
		},
		{
			name: "a vehicle heading to its last stop is judged only as the vehicle ahead",
			cars: []liveCar{{name: "F", trip: "GN", along: 400}, {name: "L", trip: "GN", along: 560}, {name: "M", trip: "GN", along: 600}},
			want: map[string]bunchedFound{"bus-F": {"bus-L", 95, 600}},
		},
		{
			name:      "a vehicle heading to the stop it turns short at is the vehicle ahead",
			shortTurn: true,
			cars:      []liveCar{{name: "F", trip: "LN", along: 480}, {name: "L", trip: "SH", along: 620}},
			want:      map[string]bunchedFound{"bus-F": {"bus-L", 83, 450}},
		},
		{
			name:      "a vehicle past its last stop is not",
			shortTurn: true,
			cars:      []liveCar{{name: "F", trip: "LN", along: 480}, {name: "L", trip: "SH", along: 640}},
			want:      map[string]bunchedFound{},
		},
		{
			name: "a vehicle that has not left its first stop is not judged",
			cars: []liveCar{{name: "F", trip: "GN", along: 25}, {name: "L", trip: "GN", along: 100}},
			want: map[string]bunchedFound{},
		},
		{
			name: "a vehicle whose latest report was off its route is not judged",
			cars: []liveCar{{name: "F", trip: "GN", along: 125, unplaced: true}, {name: "L", trip: "GN", along: 250}},
			want: map[string]bunchedFound{},
		},
		{
			name: "nor is it the vehicle ahead",
			cars: []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 250, unplaced: true}},
			want: map[string]bunchedFound{},
		},
		{
			name: "a vehicle heard from exactly ALERT_QUIET_AFTER ago is not quiet yet",
			cars: []liveCar{{name: "F", trip: "GN", along: 125, heard: -defaultAlertQuietAfter}, {name: "L", trip: "GN", along: 250}},
			want: map[string]bunchedFound{"bus-F": {"bus-L", 75, 600}},
		},
		{
			name: "a quiet vehicle is not judged",
			cars: []liveCar{{name: "F", trip: "GN", along: 125, heard: quiet}, {name: "L", trip: "GN", along: 250}},
			want: map[string]bunchedFound{},
		},
		{
			name: "a quiet vehicle ahead is passed over",
			cars: []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 250, heard: quiet}, {name: "M", trip: "GN", along: 350}},
			want: map[string]bunchedFound{"bus-F": {"bus-M", 135, 600}},
		},
		{
			name: "the vehicle ahead is the nearest",
			cars: []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 250}, {name: "M", trip: "GN", along: 300}},
			want: map[string]bunchedFound{"bus-F": {"bus-L", 75, 600}, "bus-L": {"bus-M", 30, 600}},
		},
		{
			name: "of two vehicles in one place, the vehicle_id decides which is ahead",
			cars: []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 250}, {name: "M", trip: "GN", along: 250}},
			want: map[string]bunchedFound{"bus-F": {"bus-M", 75, 600}},
		},
		{
			name: "the other way, judged against its own direction's service, every 15 minutes",
			cars: []liveCar{{name: "F", trip: "GS", along: 125}, {name: "L", trip: "GS", along: 250}},
			want: map[string]bunchedFound{"bus-F": {"bus-L", 74, 900}},
		},
		{
			name:      "a vehicle along another shape is not ahead",
			twoShapes: true,
			cars:      []liveCar{{name: "F", trip: "NA", along: 125}, {name: "V", trip: "NB", along: 200}},
			want:      map[string]bunchedFound{},
		},
		{
			name:      "one along the same shape is, against the two shapes' trips together",
			twoShapes: true,
			cars:      []liveCar{{name: "F", trip: "NA", along: 125}, {name: "L", trip: "NA", along: 200}},
			want:      map[string]bunchedFound{"bus-F": {"bus-L", 45, 300}},
		},
		{
			name: "a vehicle the other way is not ahead",
			cars: []liveCar{{name: "F", trip: "GN", along: 125}, {name: "S", trip: "GS", along: 300}},
			want: map[string]bunchedFound{},
		},
		{
			name:  "outside frequent service nothing is judged",
			clock: reportDayStart.Add(hms(9, 0, 0)),
			cars:  []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 250}},
			want:  map[string]bunchedFound{},
		},
		{
			name:  "nor is a vehicle level with the vehicle ahead",
			clock: reportDayStart.Add(hms(9, 0, 0)),
			cars:  []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 250, fix: 2 * time.Minute}},
			want:  map[string]bunchedFound{},
		},
		{
			name:  "a vehicle ahead that does not stop there is passed over",
			clock: evening,
			cars:  []liveCar{{name: "F", trip: "GN", along: 125}, {name: "T", trip: "GT", along: 250}, {name: "M", trip: "GN", along: 450}},
			want:  map[string]bunchedFound{},
		},
		{
			name:  "and the vehicle ahead of it is the one judged against",
			clock: evening,
			cars:  []liveCar{{name: "F", trip: "GN", along: 125}, {name: "T", trip: "GT", along: 250}, {name: "M", trip: "GN", along: 350}},
			want:  map[string]bunchedFound{"bus-F": {"bus-M", 135, 600}},
		},
		{
			name:  "a timetabled trip apart from the frequent service is passed over",
			clock: reportDayStart.Add(hms(7, 38, 0)),
			cars:  []liveCar{{name: "F", trip: "GN", along: 125}, {name: "X", trip: "GX", along: 250}},
			want:  map[string]bunchedFound{},
		},
		{
			name:  "and is not judged",
			clock: reportDayStart.Add(hms(7, 38, 0)),
			cars:  []liveCar{{name: "X", trip: "GX", along: 125}, {name: "F", trip: "GN", along: 250}},
			want:  map[string]bunchedFound{},
		},
		{
			name: "a run dated by its trip's start",
			cars: []liveCar{
				{name: "F", trip: "GN", along: 125, noStartDate: true, startedAt: hms(7, 18, 0)},
				{name: "L", trip: "GN", along: 250, noStartDate: true, startedAt: hms(7, 17, 0)},
			},
			want: map[string]bunchedFound{"bus-F": {"bus-L", 75, 600}},
		},
		{
			name: "a run on a day its trip does not run is not the vehicle ahead",
			cars: []liveCar{{name: "F", trip: "GN", along: 125}, {name: "L", trip: "GN", along: 250, startDate: "20260905"}},
			want: map[string]bunchedFound{},
		},
		{
			name:      "a timetable every 10 minutes: 6D 2 minutes behind 6C",
			timetable: true,
			clock:     reportDayStart.Add(hms(7, 16, 0)),
			cars:      []liveCar{{name: "6D", trip: "6D", along: 200}, {name: "6C", trip: "6C", along: 400}},
			want:      map[string]bunchedFound{"bus-6D": {"bus-6C", 120, 600}},
		},
		{
			name:      "a vehicle that turns short, 66 s behind one that goes on",
			shortTurn: true,
			cars:      []liveCar{{name: "F", trip: "SH", along: 450}, {name: "L", trip: "LN", along: 560}},
			want:      map[string]bunchedFound{"bus-F": {"bus-L", 66, 450}},
		},
		{
			name:      "the vehicle ahead less than 30 m past where the one behind turns short is at that stop",
			shortTurn: true,
			cars:      []liveCar{{name: "F", trip: "SH", along: 450}, {name: "L", trip: "LN", along: 620}},
			want:      map[string]bunchedFound{"bus-F": {"bus-L", 90, 450}},
		},
		{
			name:      "the vehicle ahead past where the one behind turns short is beyond what its timetable covers",
			shortTurn: true,
			cars:      []liveCar{{name: "F", trip: "SH", along: 450}, {name: "L", trip: "LN", along: 1000}},
			want:      map[string]bunchedFound{},
		},
		{
			name:      "a timetabled departure 13 minutes after the one before is not judged",
			timetable: true,
			clock:     reportDayStart.Add(hms(7, 57, 0)),
			cars:      []liveCar{{name: "6G", trip: "6G", along: 200}, {name: "6F", trip: "6F", along: 300}},
			want:      map[string]bunchedFound{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ix, clock := gapIx, bunchClock
			switch {
			case tc.timetable:
				ix = timetableIx
			case tc.shortTurn:
				ix = shortIx
			case tc.twoShapes:
				ix = twoShapesIx
			}
			if !tc.clock.IsZero() {
				clock = tc.clock
			}
			// Both orders: the vehicles' order along the shape, not the
			// order of the trips, decides which is ahead.
			for _, cars := range [][]liveCar{tc.cars, reversed(tc.cars)} {
				trips, states := liveCars(t, ix, clock, clock, cars...)
				got := (&stopSchedules{}).bunched(ix, trips, states, clock, defaultAlertQuietAfter)
				assert.Equal(t, tc.want, foundOf(got))
			}
		})
	}
}

func reversed(cars []liveCar) []liveCar {
	out := slices.Clone(cars)
	slices.Reverse(out)
	return out
}

// TestStopSchedules_BunchedSpacing: what is found is the vehicle ahead's trip,
// the follower's, and when the two were last heard from.
func TestStopSchedules_BunchedSpacing(t *testing.T) {
	ix := gapFixtureIndex(t)
	trips, states := liveCars(t, ix, bunchClock, bunchClock,
		liveCar{name: "F", trip: "GN", along: 125, heard: -20 * time.Second},
		liveCar{name: "L", trip: "GN", along: 250, heard: -5 * time.Second})

	got := (&stopSchedules{}).bunched(ix, trips, states, bunchClock, defaultAlertQuietAfter)

	require.Contains(t, got, "bus-F")
	assert.Equal(t, vehicleSpacing{
		tripID:    1,
		ahead:     trips[1],
		spacing:   75 * time.Second,
		scheduled: 10 * time.Minute,
		at:        bunchClock.Add(-5 * time.Second),
	}, got["bus-F"])

	trips, states = liveCars(t, ix, bunchClock, bunchClock,
		liveCar{name: "F", trip: "GN", along: 125, heard: -5 * time.Second},
		liveCar{name: "L", trip: "GN", along: 250, heard: -20 * time.Second})
	got = (&stopSchedules{}).bunched(ix, trips, states, bunchClock, defaultAlertQuietAfter)
	assert.Equal(t, bunchClock.Add(-5*time.Second), got["bus-F"].at, "the later of the two, whichever it is")
}

// TestStopSchedules_BunchedNeeds pins what the check reads: a schedule, and a
// vehicle in the tracker on the trip running.
func TestStopSchedules_BunchedNeeds(t *testing.T) {
	ix := gapFixtureIndex(t)
	trips, states := liveCars(t, ix, bunchClock, bunchClock,
		liveCar{name: "F", trip: "GN", along: 125}, liveCar{name: "L", trip: "GN", along: 250})
	require.Len(t, (&stopSchedules{}).bunched(ix, trips, states, bunchClock, defaultAlertQuietAfter), 1)

	assert.Empty(t, (&stopSchedules{}).bunched(nil, trips, states, bunchClock, defaultAlertQuietAfter), "no schedule")
	assert.Empty(t, (&stopSchedules{}).bunched(ix, trips, states[:1], bunchClock, defaultAlertQuietAfter), "the vehicle ahead not in the tracker")
	assert.Empty(t, (&stopSchedules{}).bunched(ix, trips[:1], states, bunchClock, defaultAlertQuietAfter), "the vehicle ahead on no active trip")

	onAnother := slices.Clone(trips)
	onAnother[0].GtfsTripID = "GX"
	assert.Empty(t, (&stopSchedules{}).bunched(ix, onAnother, states, bunchClock, defaultAlertQuietAfter), "the tracker has the vehicle on another trip")

	notInSchedule := []*VehicleState{ptrTo(*states[0]), states[1]}
	notInSchedule[0].TripID = "ZZ"
	unknown := slices.Clone(trips)
	unknown[0].GtfsTripID = "ZZ"
	assert.Empty(t, (&stopSchedules{}).bunched(ix, unknown, notInSchedule, bunchClock, defaultAlertQuietAfter), "a trip the schedule does not have")
}

func ptrTo[T any](v T) *T { return &v }

// TestStopSchedules_KeepsOnlyTheLatestDates: the stops laid out are kept for
// the next look, for the service dates the look used, and laid out again for
// a new schedule.
func TestStopSchedules_KeepsOnlyTheLatestDates(t *testing.T) {
	ix := gapFixtureIndex(t)
	c := &stopSchedules{}
	trips, states := liveCars(t, ix, bunchClock, bunchClock,
		liveCar{name: "F", trip: "GN", along: 125}, liveCar{name: "L", trip: "GN", along: 250})
	c.bunched(ix, trips, states, bunchClock, defaultAlertQuietAfter)
	require.Len(t, c.byStop, 1)
	var laidOut *stopSchedule
	for key, s := range c.byStop {
		assert.Equal(t, stopScheduleKey{"R5", directionKey{id: 0}, stopKey{"ST2", 0}, "20260902"}, key)
		laidOut = s
	}

	c.bunched(ix, trips, states, bunchClock, defaultAlertQuietAfter)
	for _, s := range c.byStop {
		assert.Same(t, laidOut, s, "laid out once")
	}

	nextDay := bunchClock.AddDate(0, 0, 1)
	trips, states = liveCars(t, ix, nextDay, nextDay,
		liveCar{name: "F", trip: "GN", along: 125, startDate: "20260903"}, liveCar{name: "L", trip: "GN", along: 250, startDate: "20260903"})
	c.bunched(ix, trips, states, nextDay, defaultAlertQuietAfter)
	require.Len(t, c.byStop, 1)
	for key := range c.byStop {
		assert.Equal(t, "20260903", key.date, "the day before is dropped")
	}

	refreshed := gapFixtureIndex(t)
	c.bunched(refreshed, trips, states, nextDay, defaultAlertQuietAfter)
	assert.Same(t, refreshed, c.ix)
	for _, s := range c.byStop {
		assert.NotSame(t, laidOut, s)
	}
}

// TestScheduleStops_OneStop: laid out alone, as the bunched alert lays out
// the stop a vehicle is heading to, a stop gets the scheduled gap Analytics
// judges by there.
func TestScheduleStops_OneStop(t *testing.T) {
	ix := gapFixtureIndex(t)
	var north []*rider.TripInfo
	var keys [][]stopKey
	for _, trip := range ix.TripsOnRoute("R5", "20260902") {
		if directionOf(trip) == (directionKey{id: 0}) {
			north = append(north, trip)
			keys = append(keys, stopKeys(trip.StopTimes))
		}
	}
	require.Len(t, north, 3, "GN, GT and GX")
	at := func(h, m int) time.Time { return reportDayStart.Add(hms(h, m, 0)) }

	laidOut := func(stop string) *stopSchedule {
		s := &stopSchedule{}
		scheduleStops(map[stopKey]*stopSchedule{{stop, 0}: s}, north, keys, reportDayStart)
		return s
	}
	st1, st2, st3 := laidOut("ST1"), laidOut("ST2"), laidOut("ST3")

	assert.Equal(t, 10*time.Minute, st2.gapAt(at(7, 20)), "GN every 10 minutes, reaching ST2 five minutes after it leaves")
	assert.Equal(t, 10*time.Minute, st2.gapAt(at(7, 40)), "GX's 07:40 alone is no frequent timetable")
	assert.Equal(t, 10*time.Minute, st2.gapAt(at(19, 30)), "GT turns short at ST2, so it adds nothing there")
	assert.Equal(t, 5*time.Minute, st1.gapAt(at(19, 30)), "but does at ST1")
	assert.Zero(t, st3.gapAt(at(7, 20)), "every trip ends at ST3, where nobody boards")
	assert.Empty(t, st3.trips)
}

// TestStopSchedules_BunchedOnALoop: on a loop that ends at the stop it
// starts from, a vehicle heading back to that stop is at the end of its
// trip, not the start, and is the vehicle ahead of one behind it. The
// report fixture's loop T3 (LP1, LP2, LP3, then LP1 again) runs every 10
// minutes here from 07:00 to 08:00.
func TestStopSchedules_BunchedOnALoop(t *testing.T) {
	zip := reportFixtureZip(t, nil, fixtureMember{"frequencies.txt", "trip_id,start_time,end_time,headway_secs,exact_times\nT3,07:00:00,08:00:00,600,0\n"})
	ix, err := rider.IndexFromBytes(zip, "edited", catalogNow)
	require.NoError(t, err)
	stops := mustTrip(t, ix, "T3").StopTimes
	require.Len(t, stops, 4)
	require.Equal(t, stops[0].StopID, stops[3].StopID, "the loop closes where it opens")
	between := func(i int, share float64) float64 {
		return stops[i].AlongShape + share*(stops[i+1].AlongShape-stops[i].AlongShape)
	}

	trips, states := liveCars(t, ix, bunchClock, bunchClock,
		liveCar{name: "F", trip: "T3", along: between(1, 0.8)},
		liveCar{name: "L", trip: "T3", along: between(2, 0.2)})
	require.Equal(t, stops[3].Sequence, states[1].StopSequence, "the vehicle ahead is heading back to LP1")

	got := foundOf((&stopSchedules{}).bunched(ix, trips, states, bunchClock, defaultAlertQuietAfter))
	require.Contains(t, got, "bus-F")
	assert.Equal(t, "bus-L", got["bus-F"].ahead)
	assert.Equal(t, int64(600), got["bus-F"].scheduled)
}
