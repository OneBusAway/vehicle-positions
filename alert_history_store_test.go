package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startAlertTrip starts a trip on bus-trip-1, "Bus 1", on route R1 and GTFS
// trip T1. The trips, and with them their alerts, go when the test ends.
func startAlertTrip(t *testing.T, store *Store) TripSummary {
	t.Helper()
	userID := setupTripTestData(t, store)
	trip, err := store.StartTrip(context.Background(), userID, "bus-trip-1", "R1", "T1")
	require.NoError(t, err)
	return TripSummary{ID: trip.ID, VehicleID: "bus-trip-1", VehicleLabel: "Bus 1", RouteID: "R1", GtfsTripID: "T1", StartTime: trip.StartTime}
}

// historyRow is a row of alert_history as the tests read it back.
type historyRow struct {
	ID        int64
	Kind      string
	RaisedAt  time.Time
	ClearedAt *time.Time
}

func alertHistoryRows(t *testing.T, store *Store) []historyRow {
	t.Helper()
	rows, err := store.pool.Query(context.Background(), "SELECT id, kind, raised_at, cleared_at FROM alert_history ORDER BY id")
	require.NoError(t, err)
	got, err := pgx.CollectRows(rows, pgx.RowToStructByPos[historyRow])
	require.NoError(t, err)
	return got
}

// historyTime is a time Postgres keeps as it is, in UTC.
var historyTime = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

// lostRaiseReply is the store, with the reply to each raise lost while lose
// is set: the row is opened, and the caller is told the raise failed.
type lostRaiseReply struct {
	*Store
	lose bool
}

func (s *lostRaiseReply) RaiseAlert(ctx context.Context, a alertEntry) (int64, error) {
	id, err := s.Store.RaiseAlert(ctx, a)
	if s.lose {
		return 0, errors.New("connection reset by peer")
	}
	return id, err
}

// TestStore_AlertRecorder_ARaiseWhoseReplyWasLost: the row a lost raise opened
// is cleared when its alert goes, before the rows a restart left open are
// settled and after, rather than cleared at the start, before it was raised,
// or left open.
func TestStore_AlertRecorder_ARaiseWhoseReplyWasLost(t *testing.T) {
	for _, tc := range []struct {
		name    string
		settled bool
		since   time.Time
	}{
		{"before the rows left open are settled", false, historyTime.Add(4 * time.Minute)},
		{"after", true, historyTime.Add(12 * time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captureWebhookLogs(t)
			store := newTestStore(t)
			trip := startAlertTrip(t, store)
			lossy := &lostRaiseReply{Store: store}
			source := &scriptedAlerts{}
			r := newAlertRecorder(lossy, source, historyTime, 10*time.Minute, time.Hour)
			t.Cleanup(r.Stop)
			if tc.settled {
				r.check(historyTime.Add(11 * time.Minute))
			}

			lossy.lose = true
			source.set(nil, newAlert(alertQuiet, tc.since, trip, tc.since.Add(3*time.Minute)))
			r.check(tc.since.Add(3 * time.Minute))
			lossy.lose = false
			source.set(nil)
			r.check(tc.since.Add(4 * time.Minute))
			r.check(tc.since.Add(10 * time.Minute))

			rows := alertHistoryRows(t, store)
			require.Len(t, rows, 1, "raised again, the alert gets back the row the lost raise opened")
			require.NotNil(t, rows[0].ClearedAt, "the row is not left open")
			assert.Equal(t, tc.since, rows[0].RaisedAt.UTC())
			assert.Equal(t, tc.since.Add(4*time.Minute), rows[0].ClearedAt.UTC(), "cleared at the check that found the alert gone")
		})
	}
}

func TestStore_AlertHistory_RoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	trip := startAlertTrip(t, store)

	id, err := store.RaiseAlert(ctx, newAlert(alertQuiet, historyTime, trip, historyTime.Add(4*time.Minute)))
	require.NoError(t, err)
	listed, err := store.ListClearedAlerts(ctx, "", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, listed, "an open alert is not listed as cleared")

	require.NoError(t, store.ClearAlert(ctx, id, historyTime.Add(20*time.Minute)))
	require.NoError(t, store.ClearAlert(ctx, id, historyTime.Add(40*time.Minute)), "a second clear of the row")

	listed, err = store.ListClearedAlerts(ctx, "", 10, 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	got := listed[0]
	assert.Equal(t, id, got.ID)
	assert.Equal(t, alertQuiet, got.Kind)
	assert.Equal(t, "bus-trip-1", got.VehicleID)
	assert.Equal(t, "Bus 1", got.VehicleLabel, "the vehicle's label, read with the row")
	assert.Equal(t, trip.ID, got.TripID)
	assert.Equal(t, "T1", got.GtfsTripID)
	assert.Equal(t, "R1", got.RouteID)
	assert.Equal(t, historyTime, got.RaisedAt.UTC(), "raised at the alert's since")
	assert.Equal(t, historyTime.Add(20*time.Minute), got.ClearedAt.UTC(), "a row clears once")
}

func TestStore_RaiseAlert_NotStartedNamesTheTripNotStarted(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	trip := startAlertTrip(t, store)
	a := newAlert(alertNotStarted, historyTime, trip, historyTime.Add(10*time.Minute))
	a.NextTrip = &alertNextTrip{GtfsTripID: "T2", RouteID: "R2", BlockID: "B1"}

	id, err := store.RaiseAlert(ctx, a)
	require.NoError(t, err)
	require.NoError(t, store.ClearAlert(ctx, id, historyTime.Add(15*time.Minute)))

	listed, err := store.ListClearedAlerts(ctx, alertNotStarted, 10, 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, trip.ID, listed[0].TripID, "the vehicle's last trip")
	assert.Equal(t, "T2", listed[0].GtfsTripID)
	assert.Equal(t, "R2", listed[0].RouteID)
}

// TestStore_RaiseAlert_Bunched: a bunched alert needs no schema of its own.
// Its row is the vehicle behind's trip, and the Alerts page lists it by its
// kind.
func TestStore_RaiseAlert_Bunched(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	trip := startAlertTrip(t, store)
	a := newAlert(alertBunched, historyTime, trip, historyTime.Add(4*time.Minute))
	a.Ahead = &alertAhead{VehicleID: "bus-trip-2", VehicleLabel: "Bus 2", SpacingS: 60, ScheduledGapS: 600}

	id, err := store.RaiseAlert(ctx, a)
	require.NoError(t, err)
	require.NoError(t, store.ClearAlert(ctx, id, historyTime.Add(10*time.Minute)))

	listed, err := store.ListClearedAlerts(ctx, alertBunched, 10, 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, alertBunched, listed[0].Kind)
	assert.Equal(t, "bus-trip-1", listed[0].VehicleID)
	assert.Equal(t, trip.ID, listed[0].TripID)
	assert.Equal(t, "T1", listed[0].GtfsTripID)
	assert.Equal(t, "R1", listed[0].RouteID)
	assert.Equal(t, historyTime, listed[0].RaisedAt.UTC())

	others, err := store.ListClearedAlerts(ctx, alertQuiet, 10, 0)
	require.NoError(t, err)
	assert.Empty(t, others)
}

func TestStore_RaiseAlert_KeepsTheOpenRow(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	trip := startAlertTrip(t, store)

	first, err := store.RaiseAlert(ctx, newAlert(alertNotMoving, historyTime, trip, historyTime))
	require.NoError(t, err)
	again, err := store.RaiseAlert(ctx, newAlert(alertNotMoving, historyTime.Add(5*time.Minute), trip, historyTime))
	require.NoError(t, err)
	other, err := store.RaiseAlert(ctx, newAlert(alertOffRoute, historyTime, trip, historyTime))
	require.NoError(t, err)

	assert.Equal(t, first, again, "the same alert, still open, keeps its row")
	assert.NotEqual(t, first, other, "another kind on the same trip is another alert")
	rows := alertHistoryRows(t, store)
	require.Len(t, rows, 2)
	assert.Equal(t, historyTime, rows[0].RaisedAt.UTC(), "and the time it was raised")

	require.NoError(t, store.ClearAlert(ctx, first, historyTime.Add(20*time.Minute)))
	later, err := store.RaiseAlert(ctx, newAlert(alertNotMoving, historyTime.Add(30*time.Minute), trip, historyTime))
	require.NoError(t, err)
	assert.NotEqual(t, first, later, "once cleared, the same alert raised again is a new row")
}

func TestStore_ClearAlertsLeftOpen(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	trip := startAlertTrip(t, store)
	ids := make(map[string]int64)
	for _, kind := range []string{alertQuiet, alertNotMoving, alertOffRoute} {
		id, err := store.RaiseAlert(ctx, newAlert(kind, historyTime, trip, historyTime))
		require.NoError(t, err)
		ids[kind] = id
	}
	require.NoError(t, store.ClearAlert(ctx, ids[alertOffRoute], historyTime.Add(5*time.Minute)))
	start := historyTime.Add(time.Hour)

	n, err := store.ClearAlertsLeftOpen(ctx, []int64{ids[alertQuiet]}, start)
	require.NoError(t, err)

	assert.Equal(t, int64(1), n)
	cleared := map[string]*time.Time{}
	for _, row := range alertHistoryRows(t, store) {
		cleared[row.Kind] = row.ClearedAt
	}
	assert.Nil(t, cleared[alertQuiet], "an alert kept is left open")
	require.NotNil(t, cleared[alertNotMoving])
	assert.Equal(t, start, cleared[alertNotMoving].UTC(), "one left open is cleared at the start")
	assert.Equal(t, historyTime.Add(5*time.Minute), cleared[alertOffRoute].UTC(), "one already cleared keeps its time")

	n, err = store.ClearAlertsLeftOpen(ctx, []int64{}, start)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "with none kept, every open alert is cleared")
}

func TestStore_ListClearedAlerts_NewestFirstOfAKind(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	trip := startAlertTrip(t, store)
	for i, kind := range []string{alertQuiet, alertOffRoute, alertQuiet, alertNotMoving} {
		at := historyTime.Add(time.Duration(i) * time.Hour)
		id, err := store.RaiseAlert(ctx, newAlert(kind, at, trip, at))
		require.NoError(t, err)
		require.NoError(t, store.ClearAlert(ctx, id, at.Add(10*time.Minute)))
	}
	cleared := func(kind string, limit, offset int) []string {
		t.Helper()
		listed, err := store.ListClearedAlerts(ctx, kind, limit, offset)
		require.NoError(t, err)
		var got []string
		for _, a := range listed {
			got = append(got, a.Kind+" "+a.ClearedAt.UTC().Format("15:04"))
		}
		return got
	}

	assert.Equal(t, []string{"not_moving 12:10", "quiet 11:10", "off_route 10:10", "quiet 09:10"}, cleared("", 10, 0))
	assert.Equal(t, []string{"quiet 11:10", "quiet 09:10"}, cleared(alertQuiet, 10, 0))
	assert.Equal(t, []string{"quiet 11:10", "off_route 10:10"}, cleared("", 2, 1))
	assert.Equal(t, []string{"quiet 09:10"}, cleared(alertQuiet, 1, 1))
}

func TestStore_PruneClearedAlerts(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	trip := startAlertTrip(t, store)
	old, err := store.RaiseAlert(ctx, newAlert(alertQuiet, historyTime, trip, historyTime))
	require.NoError(t, err)
	require.NoError(t, store.ClearAlert(ctx, old, historyTime.Add(10*time.Minute)))
	recent, err := store.RaiseAlert(ctx, newAlert(alertOffRoute, historyTime, trip, historyTime))
	require.NoError(t, err)
	require.NoError(t, store.ClearAlert(ctx, recent, historyTime.Add(3*time.Hour)))
	open, err := store.RaiseAlert(ctx, newAlert(alertNotMoving, historyTime, trip, historyTime))
	require.NoError(t, err)

	n, err := store.PruneClearedAlerts(ctx, historyTime.Add(time.Hour))

	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	var left []int64
	for _, row := range alertHistoryRows(t, store) {
		left = append(left, row.ID)
	}
	assert.Equal(t, []int64{recent, open}, left, "one cleared since the cutoff stays, and one open stays however old")
}

func TestAlertRecorder_RestartKeepsTheRows(t *testing.T) {
	store := newTestStore(t)
	trip := startAlertTrip(t, store)
	quiet := newAlert(alertQuiet, historyTime, trip, historyTime)
	offRoute := newAlert(alertOffRoute, historyTime, trip, historyTime)
	source := &scriptedAlerts{}

	// The first run raises both, and stops with them open.
	first := newAlertRecorder(store, source, historyTime, 11*time.Minute, time.Hour)
	source.set(nil, quiet, offRoute)
	first.check(historyTime.Add(5 * time.Minute))
	first.Stop()
	require.Len(t, alertHistoryRows(t, store), 2)

	// The next starts an hour later. Its watcher sees the quiet vehicle again
	// three minutes in, and only that.
	start := historyTime.Add(time.Hour)
	next := newAlertRecorder(store, source, start, 11*time.Minute, time.Hour)
	t.Cleanup(next.Stop)
	source.set(nil)
	next.check(start.Add(time.Minute))
	reQuiet := newAlert(alertQuiet, start, trip, start.Add(4*time.Minute))
	source.set(nil, reQuiet)
	next.check(start.Add(4 * time.Minute))

	rows := alertHistoryRows(t, store)
	require.Len(t, rows, 2, "an alert open across the restart is not raised again")
	assert.Nil(t, rows[0].ClearedAt)
	assert.Nil(t, rows[1].ClearedAt, "nor is one cleared before the watcher could see it again")
	assert.Equal(t, historyTime, rows[0].RaisedAt.UTC(), "the quiet vehicle's alert keeps when it was raised")

	next.check(start.Add(11 * time.Minute))

	rows = alertHistoryRows(t, store)
	require.Len(t, rows, 2)
	assert.Nil(t, rows[0].ClearedAt, "the quiet vehicle is still quiet")
	require.NotNil(t, rows[1].ClearedAt)
	assert.Equal(t, start, rows[1].ClearedAt.UTC(), "the alert that did not come back cleared at the start")

	source.set(nil)
	next.check(start.Add(12 * time.Minute))
	rows = alertHistoryRows(t, store)
	require.NotNil(t, rows[0].ClearedAt)
	assert.Equal(t, start.Add(12*time.Minute), rows[0].ClearedAt.UTC(), "and the kept row clears as any other")
}

// clearReplyLost is a Store whose ClearAlert fails while fail is set: after
// the clear commits with commit set, before it otherwise.
type clearReplyLost struct {
	*Store
	fail, commit bool
}

func (s *clearReplyLost) ClearAlert(ctx context.Context, id int64, at time.Time) error {
	if !s.fail {
		return s.Store.ClearAlert(ctx, id, at)
	}
	if s.commit {
		if err := s.Store.ClearAlert(ctx, id, at); err != nil {
			return err
		}
	}
	return errors.New("connection reset")
}

func TestAlertRecorder_ClearThatFailed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		commit bool
		back   bool
		want   []string
	}{
		{
			name:   "after it committed, the alert that comes back is a new row",
			commit: true,
			back:   true,
			want:   []string{"09:00-09:06", "09:07-09:08"},
		},
		{
			name: "before it committed, the alert that comes back keeps its open row",
			back: true,
			want: []string{"09:00-09:08"},
		},
		{
			name: "before it committed, the alert that stays gone clears at the next check",
			want: []string{"09:00-09:07"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captureWebhookLogs(t)
			store := newTestStore(t)
			trip := startAlertTrip(t, store)
			lost := &clearReplyLost{Store: store, commit: tc.commit}
			source := &scriptedAlerts{}
			r := newAlertRecorder(lost, source, historyTime, 11*time.Minute, time.Hour)
			t.Cleanup(r.Stop)

			source.set(nil, newAlert(alertQuiet, historyTime, trip, historyTime))
			r.check(historyTime.Add(5 * time.Minute))
			lost.fail = true
			source.set(nil)
			r.check(historyTime.Add(6 * time.Minute))
			lost.fail = false
			if tc.back {
				source.set(nil, newAlert(alertQuiet, historyTime.Add(7*time.Minute), trip, historyTime.Add(7*time.Minute)))
			}
			r.check(historyTime.Add(7 * time.Minute))
			source.set(nil)
			r.check(historyTime.Add(8 * time.Minute))

			var got []string
			for _, row := range alertHistoryRows(t, store) {
				require.NotNil(t, row.ClearedAt, "row %d left open", row.ID)
				got = append(got, row.RaisedAt.UTC().Format("15:04")+"-"+row.ClearedAt.UTC().Format("15:04"))
			}
			assert.Equal(t, tc.want, got, "each row raised-cleared")
		})
	}
}
