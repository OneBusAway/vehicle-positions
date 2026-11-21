package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStore_ServiceDays: a day saved is read back as it was, a range is read
// with both ends and oldest first, a save replaces a day's row, and pruning
// removes only the days before the cutoff's date.
func TestStore_ServiceDays(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	// Days of their own, cleared after, since the test database is shared.
	day := func(d int) time.Time { return time.Date(2031, 3, d, 0, 0, 0, 0, time.UTC) }
	clear := func() {
		_, err := store.pool.Exec(ctx, `DELETE FROM service_day_totals WHERE service_date BETWEEN $1 AND $2`, day(1), day(31))
		require.NoError(t, err)
	}
	clear()
	t.Cleanup(clear)

	busy := dayTotals{
		Version: dayTotalsVersion,
		Routes: []routeDayTotals{{RouteID: "R1", ShortName: "1", LongName: "Straight", Hours: []hourTotals{
			{Hour: 8, Scheduled: 2, Run: 1, OnTime: 5, Early: 1, Late: 2, Unknown: 1},
			{Hour: 25, Scheduled: 1},
		}, Gaps: []directionGapTotals{
			{DirectionID: ptr(0), Headsign: "North", Hours: []gapHourTotals{
				{Hour: 7, gapCounts: gapCounts{Gaps: 2, Bunched: 1, LongGaps: 1, Unknown: 1, SumGapS: 1200, SumGapSq: 1180800, SumScheduledS: 1200, SumScheduledSq: 720000}},
			}},
			{Headsign: "Loop", Hours: make([]gapHourTotals, 0)},
		}}},
		Arrivals:        []arrivalMinute{{Minute: -3, Early: 1}, {Minute: 4, OnTime: 1, Late: 1}},
		ArrivalsLeftOut: 1,
		GapRatios:       []gapRatioCount{{Tenth: 2, Gaps: 1}, {Tenth: 18, Gaps: 1}},
	}
	quiet := dayTotals{Routes: make([]routeDayTotals, 0), Arrivals: make([]arrivalMinute, 0), GapRatios: make([]gapRatioCount, 0)}
	window := func(d time.Time, totals dayTotals, late time.Duration) storedServiceDay {
		return storedServiceDay{Date: d, OnTimeEarly: time.Minute, OnTimeLate: late, Totals: totals}
	}
	require.NoError(t, store.SaveServiceDay(ctx, window(day(3), busy, 5*time.Minute)))
	require.NoError(t, store.SaveServiceDay(ctx, window(day(5), quiet, 5*time.Minute)))
	require.NoError(t, store.SaveServiceDay(ctx, window(day(7), busy, 5*time.Minute)))

	got, err := store.ListServiceDays(ctx, day(3), day(5))
	require.NoError(t, err)
	busy.Date, quiet.Date = "2031-03-03", "2031-03-05"
	assert.Equal(t, []storedServiceDay{window(day(3), busy, 5*time.Minute), window(day(5), quiet, 5*time.Minute)}, got,
		"both ends, oldest first; a day with no service is kept too")

	busy.Date = ""
	require.NoError(t, store.SaveServiceDay(ctx, window(day(5), busy, 3*time.Minute)))
	got, err = store.ListServiceDays(ctx, day(5), day(5))
	require.NoError(t, err)
	busy.Date = "2031-03-05"
	assert.Equal(t, []storedServiceDay{window(day(5), busy, 3*time.Minute)}, got, "saved again, the row is replaced")

	n, err := store.PruneServiceDays(ctx, day(5).Add(15*time.Hour))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1))
	got, err = store.ListServiceDays(ctx, day(1), day(31))
	require.NoError(t, err)
	kept := make([]time.Time, 0, len(got))
	for _, d := range got {
		kept = append(kept, d.Date)
	}
	assert.Equal(t, []time.Time{day(5), day(7)}, kept, "the cutoff's own day stays")
}

// A row whose totals do not decode, as one written before their shape changed
// would not, is left out of the range rather than failing it, and saving the
// day again replaces it.
func TestStore_ServiceDaysSkipsARowThatDoesNotDecode(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	day := func(d int) time.Time { return time.Date(2031, 4, d, 0, 0, 0, 0, time.UTC) }
	clear := func() {
		_, err := store.pool.Exec(ctx, `DELETE FROM service_day_totals WHERE service_date BETWEEN $1 AND $2`, day(1), day(30))
		require.NoError(t, err)
	}
	clear()
	t.Cleanup(clear)

	kept := func(d time.Time) storedServiceDay {
		return storedServiceDay{Date: d, OnTimeEarly: time.Minute, OnTimeLate: 5 * time.Minute,
			Totals: dayTotals{Routes: []routeDayTotals{{RouteID: "R1", Hours: []hourTotals{{Hour: 8, Scheduled: 1}}}}, Arrivals: make([]arrivalMinute, 0)}}
	}
	require.NoError(t, store.SaveServiceDay(ctx, kept(day(3))))
	require.NoError(t, store.SaveServiceDay(ctx, kept(day(5))))
	_, err := store.pool.Exec(ctx, `INSERT INTO service_day_totals (service_date, on_time_early_s, on_time_late_s, totals)
		VALUES ($1, 60, 300, '{"routes": 5}')`, day(4))
	require.NoError(t, err)

	got, err := store.ListServiceDays(ctx, day(3), day(5))
	require.NoError(t, err, "one bad row does not fail the range")
	dates := func(days []storedServiceDay) []time.Time {
		out := make([]time.Time, 0, len(days))
		for _, d := range days {
			out = append(out, d.Date)
		}
		return out
	}
	assert.Equal(t, []time.Time{day(3), day(5)}, dates(got), "the bad row is left out")

	require.NoError(t, store.SaveServiceDay(ctx, kept(day(4))))
	got, err = store.ListServiceDays(ctx, day(3), day(5))
	require.NoError(t, err)
	assert.Equal(t, []time.Time{day(3), day(4), day(5)}, dates(got), "worked out again, it is replaced")
}
