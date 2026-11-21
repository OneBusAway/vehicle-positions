package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// gapLines are the gaps at the stops of one direction's trips worked out from
// runs: a line for each, naming the stop, when the later vehicle passed it on
// the service day's clock, and the gap and the scheduled gap, or unknown.
func gapLines(t *testing.T, trips []*rider.TripInfo, runs []TripSummary, trails map[int64][]LocationPoint) []string {
	t.Helper()
	byTrip, trailsByTrip := byGTFSTrip(runs, trails)
	lines := make([]string, 0)
	for key, stop := range directionStops(trips, reportDayStart, byTrip, trailsByTrip, testReportSettings.maxShapeDistance) {
		for _, g := range stop.gaps() {
			line := key.stopID + " " + serviceClock(g.at.Sub(reportDayStart))
			if g.unknown {
				line += " unknown"
			} else {
				line += fmt.Sprintf(" %v of %v", g.gap, g.scheduled)
			}
			lines = append(lines, line)
		}
	}
	slices.Sort(lines)
	return lines
}

// byGTFSTrip keys runs and their trails by GTFS trip id, as
// buildServiceReport takes them.
func byGTFSTrip(runs []TripSummary, trails map[int64][]LocationPoint) (map[string][]TripSummary, map[string][][]LocationPoint) {
	byTrip := make(map[string][]TripSummary)
	trailsByTrip := make(map[string][][]LocationPoint)
	for _, run := range runs {
		byTrip[run.GtfsTripID] = append(byTrip[run.GtfsTripID], run)
		trailsByTrip[run.GtfsTripID] = append(trailsByTrip[run.GtfsTripID], trails[run.ID])
	}
	return byTrip, trailsByTrip
}

// TestDirectionStops_Gaps is the gap maths on GN, every 10 minutes from
// 07:00 to 08:00, from 19:00 to 20:00 and every 30 from 23:30 to 24:30,
// reaching ST2 five minutes after ST1. Its stops ST1 and ST2 are measured,
// and ST3, the last, is not. Each north run passes 30 m beyond a stop 6 s
// after leaving it.
func TestDirectionStops_Gaps(t *testing.T) {
	ix := gapFixtureIndex(t)
	gn := mustTrip(t, ix, "GN")
	run := func(name string, leave, leaveST2 time.Duration) gapRun {
		return gapRun{name: name, tripID: "GN", legs: north(leave, leaveST2)}
	}
	tests := []struct {
		name string
		runs []gapRun
		want []string
	}{
		{"on the gap",
			[]gapRun{run("A", hms(7, 0, 0), hms(7, 5, 0)), run("B", hms(7, 10, 0), hms(7, 15, 0))},
			[]string{"ST1 07:10:06 10m0s of 10m0s", "ST2 07:15:06 10m0s of 10m0s"}},
		{"bunched",
			[]gapRun{run("A", hms(7, 0, 0), hms(7, 5, 0)), run("B", hms(7, 2, 0), hms(7, 7, 0))},
			[]string{"ST1 07:02:06 2m0s of 10m0s", "ST2 07:07:06 2m0s of 10m0s"}},
		{"a wide gap",
			[]gapRun{run("A", hms(7, 0, 0), hms(7, 5, 0)), run("B", hms(7, 25, 0), hms(7, 30, 0))},
			[]string{"ST1 07:25:06 25m0s of 10m0s", "ST2 07:30:06 25m0s of 10m0s"}},
		{"the first passing of a stretch has no gap",
			[]gapRun{run("A", hms(7, 0, 0), hms(7, 5, 0))},
			[]string{}},
		{"ordered by time at each stop: B overtakes A while A waits at ST2",
			[]gapRun{run("A", hms(7, 0, 0), hms(7, 14, 0)), run("B", hms(7, 3, 0), hms(7, 8, 0))},
			[]string{"ST1 07:03:06 3m0s of 10m0s", "ST2 07:14:06 6m0s of 10m0s"}},
		{"B's phone is silent as it leaves ST2, so the gap it could fall in there is unknown",
			[]gapRun{
				run("A", hms(7, 0, 0), hms(7, 5, 0)),
				{name: "B", tripID: "GN", legs: north(hms(7, 10, 0), hms(7, 15, 0)), silent: [2]time.Duration{hms(7, 14, 0), hms(7, 16, 30)}},
				run("C", hms(7, 20, 0), hms(7, 25, 0)),
			},
			[]string{"ST1 07:10:06 10m0s of 10m0s", "ST1 07:20:06 10m0s of 10m0s", "ST2 07:25:06 unknown"}},
		{"B's trail starts past ST1, so it left ST1 between its start and its first fix",
			[]gapRun{
				run("A", hms(7, 0, 0), hms(7, 5, 0)),
				{name: "B", tripID: "GN", silent: [2]time.Duration{hms(7, 5, 59), hms(7, 6, 40)}, legs: func(trip *rider.TripInfo) []reportLeg {
					return []reportLeg{{hms(7, 6, 0), 0}, {hms(7, 7, 40), 500}, {hms(7, 12, 0), 500}, {hms(7, 13, 40), trip.Shape.Length}}
				}},
				run("C", hms(7, 10, 0), hms(7, 15, 0)),
			},
			[]string{"ST1 07:10:06 unknown", "ST2 07:12:06 7m0s of 10m0s", "ST2 07:15:06 3m0s of 10m0s"}},
		{"B turned short before ST2 and ended within two minutes of its last fix: it did not serve ST2",
			[]gapRun{
				run("A", hms(7, 0, 0), hms(7, 5, 0)),
				{name: "B", tripID: "GN", end: hms(7, 13, 0), legs: func(*rider.TripInfo) []reportLeg {
					return []reportLeg{{hms(7, 9, 0), 0}, {hms(7, 10, 0), 0}, {hms(7, 11, 0), 300}}
				}},
				run("C", hms(7, 20, 0), hms(7, 25, 0)),
			},
			[]string{"ST1 07:10:06 10m0s of 10m0s", "ST1 07:20:06 10m0s of 10m0s", "ST2 07:25:06 20m0s of 10m0s"}},
		{"B's trail ends short of ST2 but its trip went on: it may have passed it",
			[]gapRun{
				run("A", hms(7, 0, 0), hms(7, 5, 0)),
				{name: "B", tripID: "GN", end: hms(7, 13, 1), legs: func(*rider.TripInfo) []reportLeg {
					return []reportLeg{{hms(7, 9, 0), 0}, {hms(7, 10, 0), 0}, {hms(7, 11, 0), 300}}
				}},
				run("C", hms(7, 20, 0), hms(7, 25, 0)),
			},
			[]string{"ST1 07:10:06 10m0s of 10m0s", "ST1 07:20:06 10m0s of 10m0s", "ST2 07:25:06 unknown"}},
		{"B sent no fix at all: unknown from its start to its end",
			[]gapRun{
				run("A", hms(7, 0, 0), hms(7, 5, 0)),
				{name: "B", tripID: "GN", noFixes: true, legs: func(*rider.TripInfo) []reportLeg {
					return []reportLeg{{hms(7, 9, 0), 0}, {hms(7, 29, 0), 0}}
				}},
				run("C", hms(7, 20, 0), hms(7, 25, 0)),
			},
			[]string{"ST1 07:20:06 unknown", "ST2 07:25:06 unknown"}},
		{"B is still running, waiting at ST1: it may pass either stop up to 100 minutes after its last fix",
			[]gapRun{
				run("A", hms(7, 0, 0), hms(7, 5, 0)),
				{name: "B", tripID: "GN", running: true, legs: func(*rider.TripInfo) []reportLeg {
					return []reportLeg{{hms(7, 4, 0), 0}, {hms(7, 6, 0), 0}}
				}},
				run("C", hms(7, 10, 0), hms(7, 15, 0)),
				run("D", hms(7, 20, 0), hms(7, 25, 0)),
			},
			[]string{"ST1 07:10:06 unknown", "ST1 07:20:06 unknown", "ST2 07:15:06 unknown", "ST2 07:25:06 unknown"}},
		{"window edges: in from the stretch's start, out from its end a headway after the window's",
			[]gapRun{
				run("before", hms(6, 55, 0), hms(7, 0, 0)),
				run("first", hms(6, 59, 54), hms(7, 4, 54)),
				run("late", hms(7, 50, 0), hms(7, 55, 0)),
				run("last", hms(8, 0, 0), hms(8, 5, 0)),
				run("after", hms(8, 9, 54), hms(8, 14, 54)),
			},
			[]string{"ST1 07:50:06 50m6s of 10m0s", "ST1 08:00:06 10m0s of 10m0s", "ST2 07:55:06 50m6s of 10m0s", "ST2 08:05:06 10m0s of 10m0s"}},
		{"two runs before any service is due have no gap",
			[]gapRun{run("A", hms(6, 40, 0), hms(6, 45, 0)), run("B", hms(6, 50, 0), hms(6, 55, 0))},
			[]string{}},
		{"the evening's first run is not a gap after the morning's last",
			[]gapRun{run("A", hms(7, 50, 0), hms(7, 55, 0)), run("B", hms(19, 0, 0), hms(19, 5, 0))},
			[]string{}},
		{"a run that crosses midnight, on the service day's clock, in the 23:30 window's last headway",
			[]gapRun{run("I", hms(23, 58, 0), hms(24, 3, 0)), run("J", hms(24, 31, 0), hms(24, 36, 0))},
			[]string{"ST1 24:31:06 33m0s of 30m0s", "ST2 24:36:06 33m0s of 30m0s"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runs, trails := gapTrips(t, ix, 1, tc.runs)
			assert.Equal(t, tc.want, gapLines(t, []*rider.TripInfo{gn}, runs, trails))
		})
	}
}

// TestDirectionStops_ForgottenTrip: B leaves ST1 and its phone goes silent a
// minute later, short of ST2. A run on its way would have left ST2 within
// GN's 10-minute run time and passingLateGrace of that last fix, so a trip
// left running, or ended hours later, holds the gaps at ST2 unknown only
// until then, while a run late on its way still holds them until it ended.
// In the evening stretch P leaves ST2 at 19:05:00, C at 19:06:00 and D at
// 19:15:06; P leaves ST1 before the stretch there, so ST1's first gap is C
// to D.
func TestDirectionStops_ForgottenTrip(t *testing.T) {
	ix := gapFixtureIndex(t)
	gn := mustTrip(t, ix, "GN")
	evening := []gapRun{
		{name: "P", tripID: "GN", legs: north(hms(18, 59, 0), hms(19, 4, 54))},
		{name: "C", tripID: "GN", legs: north(hms(19, 0, 0), hms(19, 5, 54))},
		{name: "D", tripID: "GN", legs: north(hms(19, 10, 0), hms(19, 15, 0))},
	}
	// silentAfter leaves ST1 at leave and is last seen a minute later, 300 m
	// along, short of ST2.
	silentAfter := func(leave time.Duration) func(*rider.TripInfo) []reportLeg {
		return func(*rider.TripInfo) []reportLeg {
			return []reportLeg{{leave - time.Minute, 0}, {leave, 0}, {leave + time.Minute, 300}}
		}
	}
	tests := []struct {
		name string
		b    gapRun
		want []string
	}{
		{"last seen at 17:26 and left running: ST2 is unknown up to 19:06, 100 minutes on, and judged after",
			gapRun{legs: silentAfter(hms(17, 25, 0)), running: true},
			[]string{"ST1 19:10:06 10m0s of 10m0s", "ST2 19:06:00 unknown", "ST2 19:15:06 9m6s of 10m0s"}},
		{"last seen at 17:26 and ended at 22:00: the same",
			gapRun{legs: silentAfter(hms(17, 25, 0)), end: hms(22, 0, 0)},
			[]string{"ST1 19:10:06 10m0s of 10m0s", "ST2 19:06:00 unknown", "ST2 19:15:06 9m6s of 10m0s"}},
		{"last seen at 17:36 and ended at 19:15, late on its way: ST2 is unknown up to its end",
			gapRun{legs: silentAfter(hms(17, 35, 0)), end: hms(19, 15, 0)},
			[]string{"ST1 19:10:06 10m0s of 10m0s", "ST2 19:06:00 unknown", "ST2 19:15:06 unknown"}},
		{"last seen at 19:05:30 and ended within two minutes of it: it turned short and did not serve ST2",
			gapRun{legs: silentAfter(hms(19, 4, 30)), end: hms(19, 7, 0)},
			[]string{"ST1 19:04:36 4m30s of 10m0s", "ST1 19:10:06 5m30s of 10m0s", "ST2 19:06:00 1m0s of 10m0s", "ST2 19:15:06 9m6s of 10m0s"}},
		{"sent no fix from its start at 17:26 and left running: both stops are unknown up to 19:06",
			gapRun{noFixes: true, running: true, legs: func(*rider.TripInfo) []reportLeg {
				return []reportLeg{{hms(17, 26, 0), 0}, {hms(17, 30, 0), 0}}
			}},
			[]string{"ST1 19:10:06 unknown", "ST2 19:06:00 unknown", "ST2 19:15:06 9m6s of 10m0s"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.b
			b.name, b.tripID = "B", "GN"
			runs, trails := gapTrips(t, ix, 1, append([]gapRun{b}, evening...))
			assert.Equal(t, tc.want, gapLines(t, []*rider.TripInfo{gn}, runs, trails))
		})
	}
}

// TestDirectionStops_PatternsShareAStop: GT turns short at ST2, so at ST1,
// which both patterns leave every 10 minutes, a vehicle is due every 5, and
// at ST2, which GT only reaches, every 10, with GT's run not in the order.
func TestDirectionStops_PatternsShareAStop(t *testing.T) {
	ix := gapFixtureIndex(t)
	runs, trails := gapTrips(t, ix, 1, []gapRun{
		{name: "P", tripID: "GN", legs: north(hms(19, 0, 0), hms(19, 5, 0))},
		{name: "Q", tripID: "GT", legs: north(hms(19, 6, 0), hms(19, 11, 0))},
		{name: "R", tripID: "GN", legs: north(hms(19, 10, 0), hms(19, 15, 0))},
	})
	assert.Equal(t, []string{"ST1 19:06:06 6m0s of 5m0s", "ST1 19:10:06 4m0s of 5m0s", "ST2 19:15:06 10m0s of 10m0s"},
		gapLines(t, []*rider.TripInfo{mustTrip(t, ix, "GN"), mustTrip(t, ix, "GT")}, runs, trails))
}

// TestDirectionStops_MeasuredStops: where the feed marks timepoints only
// those are measured, and a trip whose shape was drawn through its stops is
// not placed, so its runs pass every stop unknown.
func TestDirectionStops_MeasuredStops(t *testing.T) {
	ix := gapFixtureIndex(t)
	runs, trails := gapTrips(t, ix, 1, []gapRun{
		{name: "A", tripID: "GN", legs: north(hms(7, 0, 0), hms(7, 5, 0))},
		{name: "B", tripID: "GN", legs: north(hms(7, 10, 0), hms(7, 15, 0))},
		{name: "C", tripID: "GN", legs: north(hms(7, 20, 0), hms(7, 25, 0))},
	})

	timepoints := *mustTrip(t, ix, "GN")
	timepoints.StopTimes = slices.Clone(timepoints.StopTimes)
	timepoints.StopTimes[1].Timepoint = false
	assert.Equal(t, []string{"ST1 07:10:06 10m0s of 10m0s", "ST1 07:20:06 10m0s of 10m0s"},
		gapLines(t, []*rider.TripInfo{&timepoints}, runs, trails), "ST2 is not a timepoint and ST3 is the last stop")
	assert.Equal(t, []string{"ST1 07:10:06 10m0s of 10m0s", "ST1 07:20:06 10m0s of 10m0s"},
		gapLines(t, []*rider.TripInfo{&timepoints, mustTrip(t, ix, "GT")}, runs, trails),
		"GT judges ST2, but as its last stop, which is not measured")

	drawn := *mustTrip(t, ix, "GN")
	drawn.ID, drawn.ShapeFromStops = "GN-drawn", true
	runs[1].GtfsTripID = drawn.ID
	assert.Equal(t, []string{"ST1 07:20:06 unknown", "ST2 07:25:06 unknown"},
		gapLines(t, []*rider.TripInfo{mustTrip(t, ix, "GN"), &drawn}, runs, trails), "B ran the trip drawn through its stops")
}

// TestRouteGaps_HoursInOrder: a direction's hours come out in order however
// its stops and hours are walked, which differs from one run to the next.
func TestRouteGaps_HoursInOrder(t *testing.T) {
	ix := gapFixtureIndex(t)
	rep := &serviceReport{Date: "2026-09-02", location: ix.Timezone()}
	runs, trails := gapTrips(t, ix, 1, gapDayRuns)
	byTrip, trailsByTrip := byGTFSTrip(runs, trails)
	for range 20 {
		gaps := routeGaps(rep, []*rider.TripInfo{mustTrip(t, ix, "GN")}, reportDayStart, byTrip, trailsByTrip, testReportSettings)
		require.Len(t, gaps, 1)
		hours := make([]int, 0, len(gaps[0].hours))
		for _, h := range gaps[0].hours {
			hours = append(hours, h.Hour)
		}
		require.Equal(t, []int{7, 8, 24}, hours)
	}
}

func TestStopKeys(t *testing.T) {
	ix := reportFixtureIndex(t)
	assert.Equal(t, []stopKey{{"LP1", 0}, {"LP2", 0}, {"LP3", 0}, {"LP1", 1}}, stopKeys(mustTrip(t, ix, "T3").StopTimes),
		"the loop's last stop is its first visited again")
	assert.Equal(t, []stopKey{{"ST1", 0}, {"ST2", 0}, {"ST3", 0}}, stopKeys(mustTrip(t, ix, "T1").StopTimes))
}

func TestRunPassing(t *testing.T) {
	base := reportDayStart.Add(hms(7, 0, 0))
	at := func(s int) time.Time { return base.Add(time.Duration(s) * time.Second) }
	fix := func(s int, along float64) placedFix { return placedFix{at: at(s), along: along} }
	ended := func(s int) *time.Time { e := at(s); return &e }
	// A run is held open for 20 minutes after its last fix.
	const open = 20 * time.Minute
	tests := []struct {
		name   string
		fixes  []placedFix
		end    *time.Time
		want   stopPassing
		served bool
	}{
		{"known, as passedAt works it out", []placedFix{fix(0, 0), fix(10, 50)}, ended(600), stopPassing{at: at(6)}, true},
		{"fixes too far apart either side: between them", []placedFix{fix(0, 0), fix(200, 100)}, ended(600), stopPassing{from: at(0), to: at(200)}, true},
		{"the trail starts past it: between the trip's start and its first fix", []placedFix{fix(60, 100), fix(70, 150)}, ended(600), stopPassing{from: at(-30), to: at(60)}, true},
		{"the trail ends short and the trip ended two minutes after: it did not serve it", []placedFix{fix(0, 0), fix(10, 20)}, ended(130), stopPassing{}, false},
		{"the trail ends short and the trip ended later: between its last fix and its end", []placedFix{fix(0, 0), fix(10, 20)}, ended(131), stopPassing{from: at(10), to: at(131)}, true},
		{"the trail ends short and the trip ended a second inside open: at its end", []placedFix{fix(0, 0), fix(10, 20)}, ended(1209), stopPassing{from: at(10), to: at(1209)}, true},
		{"the trail ends short and the trip ended a second after open: open after its last fix", []placedFix{fix(0, 0), fix(10, 20)}, ended(1211), stopPassing{from: at(10), to: at(1210)}, true},
		{"the trail ends short and the trip ended hours later: open after its last fix", []placedFix{fix(0, 0), fix(10, 20)}, ended(5 * 3600), stopPassing{from: at(10), to: at(1210)}, true},
		{"the trail ends short and the trip runs on: open after its last fix", []placedFix{fix(0, 0), fix(10, 20)}, nil, stopPassing{from: at(10), to: at(1210)}, true},
		{"no fixes, the trip ended: between its start and end", nil, ended(60), stopPassing{from: at(-30), to: at(60)}, true},
		{"no fixes, the trip ended hours later: open after its start", nil, ended(5 * 3600), stopPassing{from: at(-30), to: at(1170)}, true},
		{"no fixes, the trip runs on: open after its start", nil, nil, stopPassing{from: at(-30), to: at(1170)}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			run := TripSummary{StartTime: at(-30), EndTime: tc.end}
			got, served := runPassing(tc.fixes, 30, run, open)
			require.Equal(t, tc.served, served)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestStopPassing_Between(t *testing.T) {
	at := func(m int) time.Time { return reportDayStart.Add(time.Duration(m) * time.Minute) }
	a, b := at(10), at(20)
	tests := []struct {
		name string
		p    stopPassing
		want bool
	}{
		{"inside", stopPassing{from: at(12), to: at(14)}, true},
		{"across both", stopPassing{from: at(5), to: at(25)}, true},
		{"ends as the gap starts", stopPassing{from: at(5), to: at(10)}, false},
		{"starts as the gap ends", stopPassing{from: at(20), to: at(25)}, false},
		{"ends a second into it", stopPassing{from: at(5), to: at(10).Add(time.Second)}, true},
		{"starts a second before it ends", stopPassing{from: at(20).Add(-time.Second), to: at(25)}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.p.between(a, b))
		})
	}
}

func TestGapCounts_Add(t *testing.T) {
	judged := func(gap, scheduled time.Duration) stopGap { return stopGap{gap: gap, scheduled: scheduled} }
	tests := []struct {
		name string
		gap  stopGap
		want gapCounts
	}{
		{"under a quarter of the scheduled gap is bunched", judged(149*time.Second, 10*time.Minute),
			gapCounts{Gaps: 1, Bunched: 1, SumGapS: 149, SumGapSq: 149 * 149, SumScheduledS: 600, SumScheduledSq: 360000}},
		{"exactly a quarter is not", judged(150*time.Second, 10*time.Minute),
			gapCounts{Gaps: 1, SumGapS: 150, SumGapSq: 22500, SumScheduledS: 600, SumScheduledSq: 360000}},
		{"two vehicles in the same second", judged(0, 10*time.Minute),
			gapCounts{Gaps: 1, Bunched: 1, SumScheduledS: 600, SumScheduledSq: 360000}},
		{"exactly half again is not long", judged(15*time.Minute, 10*time.Minute),
			gapCounts{Gaps: 1, SumGapS: 900, SumGapSq: 810000, SumScheduledS: 600, SumScheduledSq: 360000}},
		{"a second more is long", judged(15*time.Minute+time.Second, 10*time.Minute),
			gapCounts{Gaps: 1, LongGaps: 1, SumGapS: 901, SumGapSq: 811801, SumScheduledS: 600, SumScheduledSq: 360000}},
		{"unknown counts nothing else", stopGap{unknown: true}, gapCounts{Unknown: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c gapCounts
			c.add(tc.gap)
			assert.Equal(t, tc.want, c)
		})
	}

	var c gapCounts
	c.add(judged(2*time.Minute, 10*time.Minute))
	c.add(judged(18*time.Minute, 10*time.Minute))
	c.add(stopGap{unknown: true})
	assert.Equal(t, gapCounts{Gaps: 2, Bunched: 1, LongGaps: 1, Unknown: 1, SumGapS: 1200, SumGapSq: 14400 + 1166400, SumScheduledS: 1200, SumScheduledSq: 720000}, c,
		"counts and sums add up")
}

func TestGapCounts_ExtraWait(t *testing.T) {
	ten := 10 * time.Minute
	tests := []struct {
		name string
		gaps []time.Duration
		want *int
	}{
		{"none judged", nil, nil},
		{"every gap as scheduled", []time.Duration{ten, ten}, ptr(0)},
		// (120^2 + 1080^2) / (2 * 1200) = 492, against 300 scheduled.
		{"a vehicle bunched behind another, then a long gap", []time.Duration{2 * time.Minute, 18 * time.Minute}, ptr(192)},
		// (700^2 + 500^2) / (2 * 1200) = 308.3.
		{"to the second", []time.Duration{700 * time.Second, 500 * time.Second}, ptr(8)},
		{"two vehicles in the same second: nobody waited", []time.Duration{0}, ptr(-300)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c gapCounts
			for _, g := range tc.gaps {
				c.add(stopGap{gap: g, scheduled: ten})
			}
			c.add(stopGap{unknown: true})
			assert.Equal(t, tc.want, c.extraWait())
		})
	}
}

func ptr(n int) *int { return &n }

func TestGapRatioTenth(t *testing.T) {
	ten := 10 * time.Minute
	tests := []struct {
		gap  time.Duration
		want int
	}{
		{0, 0},
		{59 * time.Second, 0},
		{time.Minute, 1},
		{ten, 10},
		{3*ten - time.Second, 29},
		{3 * ten, 30},
		{2 * time.Hour, 30},
	}
	for _, tc := range tests {
		t.Run(tc.gap.String(), func(t *testing.T) {
			assert.Equal(t, tc.want, gapRatioTenth(stopGap{gap: tc.gap, scheduled: ten}))
		})
	}
}

func TestMostOften(t *testing.T) {
	assert.Nil(t, mostOften(map[time.Duration]int{}))
	assert.Equal(t, ptr(600), mostOften(map[time.Duration]int{10 * time.Minute: 3, 15 * time.Minute: 1}))
	assert.Equal(t, ptr(300), mostOften(map[time.Duration]int{10 * time.Minute: 2, 5 * time.Minute: 2, 15 * time.Minute: 2, 20 * time.Minute: 1}),
		"the shortest of those seen as often")
}

// TestRouteGaps_Directions: a route's headway trips are grouped by
// direction_id, or by headsign where the feed gives none, in that order;
// each direction is listed even with nothing run, and the route's timetabled
// trips are left out.
func TestRouteGaps_Directions(t *testing.T) {
	ix := gapFixtureIndex(t)
	rep := &serviceReport{Date: "2026-09-02", location: ix.Timezone()}
	headsign := func(id, sign string, direction int) *rider.TripInfo {
		trip := *mustTrip(t, ix, "GN")
		trip.ID, trip.Headsign, trip.DirectionID = id, sign, direction
		return &trip
	}
	trips := []*rider.TripInfo{
		mustTrip(t, ix, "GS"),
		headsign("GD", "Downtown", -1),
		mustTrip(t, ix, "GN"),
		headsign("GV", "Northgate", 0),
		headsign("GA", "Airport", -1),
		mustTrip(t, ix, "GX"),
	}
	none := `"gaps_judged":0,"bunched":0,"long_gaps":0,"unknown":0,"extra_wait_s":null,"scheduled_gap_s":null`
	// The directions are grouped in a map, so the order is worked out many
	// times over.
	for range 20 {
		gaps := routeGaps(rep, trips, reportDayStart, nil, nil, testReportSettings)
		encoded, err := json.Marshal(gaps)
		require.NoError(t, err)
		require.JSONEq(t, `[
			{"direction_id":null,"headsign":"Airport",`+none+`},
			{"direction_id":null,"headsign":"Downtown",`+none+`},
			{"direction_id":0,"headsign":"North",`+none+`},
			{"direction_id":1,"headsign":"South",`+none+`}
		]`, string(encoded))
		for _, d := range gaps {
			require.NotNil(t, d.hours, "kept as an empty list")
		}
	}

	assert.Empty(t, routeGaps(rep, []*rider.TripInfo{mustTrip(t, ix, "GX")}, reportDayStart, nil, nil, testReportSettings),
		"a route run only by timetable")
}
