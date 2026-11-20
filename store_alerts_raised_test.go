package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// periodStart is the start of the period these tests read, a year no other
// test writes alerts in.
var periodStart = time.Date(2031, 3, 10, 10, 0, 0, 0, time.UTC)

// insertAlert writes a row of alert_history for trip as it stands, open when
// cleared is nil.
func insertAlert(t *testing.T, store *Store, trip TripSummary, kind, routeID string, raised time.Time, cleared *time.Time) {
	t.Helper()
	_, err := store.pool.Exec(context.Background(),
		`INSERT INTO alert_history (kind, vehicle_id, trip_id, gtfs_trip_id, route_id, raised_at, cleared_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		kind, trip.VehicleID, trip.ID, raised.Format(time.RFC3339Nano), routeID, raised, cleared)
	require.NoError(t, err)
}

func TestStore_ListAlertsRaisedBetween(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	trip := startAlertTrip(t, store)
	from, to := periodStart, periodStart.Add(7*24*time.Hour)
	at := func(d time.Duration) *time.Time { v := from.Add(d); return &v }

	// Raised before the period and cleared in it: not the period's.
	insertAlert(t, store, trip, alertQuiet, "R1", from.Add(-time.Minute), at(time.Hour))
	insertAlert(t, store, trip, alertOffRoute, "R2", from, at(20*time.Minute))
	insertAlert(t, store, trip, alertNotStarted, "R3", from.Add(time.Hour), nil)
	// Raised in the period and cleared after it.
	insertAlert(t, store, trip, alertNotMoving, "R1", to.Add(-time.Minute), at(8*24*time.Hour))
	insertAlert(t, store, trip, alertQuiet, "R1", to, at(7*24*time.Hour+time.Minute))
	insertAlert(t, store, trip, alertQuiet, "R1", to.Add(time.Hour), nil)

	got, err := store.ListAlertsRaisedBetween(ctx, from, to)
	require.NoError(t, err)
	require.Len(t, got, 3)

	assert.Equal(t, alertOffRoute, got[0].Kind)
	assert.Equal(t, "R2", got[0].RouteID)
	assert.Equal(t, from, got[0].RaisedAt.UTC(), "raised at the period's start is in it")
	require.NotNil(t, got[0].ClearedAt)
	assert.Equal(t, from.Add(20*time.Minute), got[0].ClearedAt.UTC())

	assert.Equal(t, alertNotStarted, got[1].Kind)
	assert.Equal(t, "R3", got[1].RouteID)
	assert.Nil(t, got[1].ClearedAt, "an open alert has no clear time")

	assert.Equal(t, alertNotMoving, got[2].Kind)
	require.NotNil(t, got[2].ClearedAt)
	assert.Equal(t, from.Add(8*24*time.Hour), got[2].ClearedAt.UTC(), "cleared after the period, still the period's")
}

func TestStore_ListAlertsRaisedBetween_Empty(t *testing.T) {
	store := newTestStore(t)
	got, err := store.ListAlertsRaisedBetween(context.Background(), periodStart, periodStart.Add(24*time.Hour))
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// TestStore_AlertHistoryRaisedIndexExists guards the index
// ListAlertsRaisedBetween reads through. Its other tests pass without it,
// since a sequential scan finds the same rows.
func TestStore_AlertHistoryRaisedIndexExists(t *testing.T) {
	store := newTestStore(t)
	var indexDef string
	err := store.pool.QueryRow(context.Background(),
		`SELECT indexdef FROM pg_indexes
		 WHERE schemaname = current_schema() AND tablename = 'alert_history' AND indexname = 'idx_alert_history_raised'`,
	).Scan(&indexDef)
	require.NoError(t, err, "migrations must create idx_alert_history_raised on alert_history")
	assert.Contains(t, indexDef, "USING btree (raised_at)")
}

// planNode is the part of a node of EXPLAIN (FORMAT JSON) the test reads.
type planNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	IndexName    string     `json:"Index Name"`
	ActualRows   float64    `json:"Actual Rows"`
	RemovedRows  float64    `json:"Rows Removed by Filter"`
	Plans        []planNode `json:"Plans"`
}

func (n planNode) walk(visit func(planNode)) {
	visit(n)
	for _, child := range n.Plans {
		child.walk(visit)
	}
}

// TestStore_ListAlertsRaisedBetween_ReadsOnlyThePeriod runs the query under
// EXPLAIN ANALYZE over half a year of alerts: alert_history is read through
// idx_alert_history_raised, never scanned whole, and the rows read are the
// week's.
func TestStore_ListAlertsRaisedBetween_ReadsOnlyThePeriod(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	trip := startAlertTrip(t, store)

	// Ten cleared alerts a day for 180 days from periodStart.
	_, err := store.pool.Exec(ctx,
		`INSERT INTO alert_history (kind, vehicle_id, trip_id, gtfs_trip_id, route_id, raised_at, cleared_at)
		 SELECT 'quiet', $1, $2, 'T' || n, 'R1', $3::timestamptz + n * interval '144 minutes',
		        $3::timestamptz + n * interval '144 minutes' + interval '5 minutes'
		 FROM generate_series(0, 1799) AS n`,
		trip.VehicleID, trip.ID, periodStart)
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "ANALYZE alert_history")
	require.NoError(t, err)

	from := periodStart.Add(90 * 24 * time.Hour)
	to := from.Add(7 * 24 * time.Hour)
	var plan []struct {
		Plan planNode `json:"Plan"`
	}
	var raw []byte
	err = store.pool.QueryRow(ctx, "EXPLAIN (ANALYZE, FORMAT JSON) "+listAlertsRaisedBetweenSQL, from, to).Scan(&raw)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &plan))
	require.Len(t, plan, 1)

	var scans []planNode
	plan[0].Plan.walk(func(n planNode) {
		if n.RelationName == "alert_history" || n.IndexName != "" {
			scans = append(scans, n)
		}
	})
	require.NotEmpty(t, scans, "the plan reads alert_history")
	read := 0.0
	for _, n := range scans {
		assert.NotEqual(t, "Seq Scan", n.NodeType, "alert_history is never scanned whole")
		if n.IndexName != "" {
			assert.Equal(t, "idx_alert_history_raised", n.IndexName)
			read = n.ActualRows
		}
		assert.Zero(t, n.RemovedRows, "no row outside the period is read and thrown away")
	}
	assert.Equal(t, 70.0, read, "a week of ten a day")
}
