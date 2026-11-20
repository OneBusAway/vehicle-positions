package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// feedHealthTestHour is an hour of the database's own for these tests, cleared
// before and after them, since the test database is shared.
var feedHealthTestHour = time.Date(2031, 4, 10, 12, 0, 0, 0, time.UTC)

func clearFeedHealthHours(t *testing.T, store *Store) {
	t.Helper()
	clear := func() {
		_, err := store.pool.Exec(context.Background(), `DELETE FROM feed_health_hours WHERE hour BETWEEN $1 AND $2`,
			feedHealthTestHour.Add(-30*24*time.Hour), feedHealthTestHour.Add(30*24*time.Hour))
		require.NoError(t, err)
	}
	clear()
	t.Cleanup(clear)
}

// TestStore_FeedHealthHours: an hour saved is read back as it was, a range is
// read with both ends and oldest first, a save replaces the hour's row, and
// pruning removes only the hours before the cutoff.
func TestStore_FeedHealthHours(t *testing.T) {
	store := newTestStore(t)
	clearFeedHealthHours(t, store)
	ctx := context.Background()
	at := func(h int) time.Time { return feedHealthTestHour.Add(time.Duration(h) * time.Hour) }

	busy := func(h int) healthHour {
		return healthHour{
			Hour:     at(h),
			Reports:  reportOutcomes{Accepted: 3600, Invalid: 2, Unauthorized: 3, RateLimited: 4, ServerError: 5},
			Feed:     feedOutcomes{Served: 1800, Refused: 6, Invalid: 7, ServerError: 8},
			Vehicles: 12,
		}
	}
	quiet := healthHour{Hour: at(1)}
	require.NoError(t, store.SaveFeedHealthHour(ctx, busy(0)))
	require.NoError(t, store.SaveFeedHealthHour(ctx, quiet))
	require.NoError(t, store.SaveFeedHealthHour(ctx, busy(3)))

	got, err := store.ListFeedHealthHours(ctx, at(0), at(1))
	require.NoError(t, err)
	assert.Equal(t, []healthHour{busy(0), quiet}, got, "both ends, oldest first; an hour of zeros is kept too")

	again := busy(1)
	again.Reports.Accepted = 1
	require.NoError(t, store.SaveFeedHealthHour(ctx, again))
	got, err = store.ListFeedHealthHours(ctx, at(1), at(1))
	require.NoError(t, err)
	assert.Equal(t, []healthHour{again}, got, "saved again, the row is replaced")

	n, err := store.PruneFeedHealthHours(ctx, at(1).Add(30*time.Minute))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(2))
	got, err = store.ListFeedHealthHours(ctx, at(-24), at(24))
	require.NoError(t, err)
	assert.Equal(t, []healthHour{busy(3)}, got, "only the hours that started before the cutoff go")
}

// TestFeedHealthHours_RestartReadsHistoryBack runs a server for two hours,
// stops it, and starts another: the second lists the first's hours from the
// database, and carries on the hour the first stopped in.
func TestFeedHealthHours_RestartReadsHistoryBack(t *testing.T) {
	store := newTestStore(t)
	clearFeedHealthHours(t, store)
	ctx := context.Background()
	clock := newHealthTestClock(feedHealthTestHour.Add(20 * time.Minute))

	first := newFeedHealth(clock.now)
	writer := startHealthHours(ctx, first, store, time.Hour)
	for range 5 {
		first.record(healthReports, http.StatusCreated)
	}
	first.record(healthFeed, http.StatusUnauthorized)
	sendReport(t, first, liveReportPath, "bus-1", http.StatusCreated)
	clock.advance(time.Hour)
	first.record(healthReports, http.StatusInternalServerError)
	writer.Stop()

	clock.advance(5 * time.Minute)
	second := newFeedHealth(clock.now)
	writer = startHealthHours(ctx, second, store, time.Hour)
	t.Cleanup(writer.Stop)
	second.record(healthReports, http.StatusCreated)

	hours, err := second.hourly(ctx, 1)
	require.NoError(t, err)
	require.Len(t, hours, healthDayHours)
	assert.Equal(t, healthHour{
		Hour:     feedHealthTestHour,
		Reports:  reportOutcomes{Accepted: 6},
		Feed:     feedOutcomes{Refused: 1},
		Vehicles: 1,
	}, hours[healthDayHours-2], "the hour the first server closed")
	assert.Equal(t, healthHour{
		Hour:    feedHealthTestHour.Add(time.Hour),
		Reports: reportOutcomes{Accepted: 1, ServerError: 1},
	}, hours[healthDayHours-1], "the hour it stopped in, carried on")
	assert.Equal(t, healthHour{Hour: feedHealthTestHour.Add(-time.Hour)}, hours[healthDayHours-3])

	clock.advance(time.Hour)
	second.saveClosedHours(ctx)
	got, err := store.ListFeedHealthHours(ctx, feedHealthTestHour.Add(time.Hour), feedHealthTestHour.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, []healthHour{hours[healthDayHours-1]}, got, "saved at its close with both servers' counts")
}
