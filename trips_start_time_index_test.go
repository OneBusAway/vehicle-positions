package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStore_TripsStartTimeIndexExists guards the index ListTripsStartedBetween
// relies on. Its tests pass without it, since a sequential scan finds the same
// trips, so only this test notices the migration going missing.
func TestStore_TripsStartTimeIndexExists(t *testing.T) {
	store := newTestStore(t)

	var indexDef string
	err := store.pool.QueryRow(context.Background(),
		`SELECT indexdef FROM pg_indexes
		 WHERE schemaname = current_schema() AND tablename = 'trips' AND indexname = 'idx_trips_start_time'`,
	).Scan(&indexDef)
	require.NoError(t, err, "migrations must create idx_trips_start_time on trips")
	assert.Contains(t, indexDef, "USING btree (start_time)")
}
