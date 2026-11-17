package main

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seed_dev.sql is loaded by hand (docs/development.md) and before the app
// smoke tests, never by the server, so nothing else notices when a migration
// leaves its ON CONFLICT targets without a matching unique index. It runs
// twice, in a transaction that rolls back, so the test database keeps none
// of its rows.
func TestSeedDev_LoadsOnMigratedDatabase(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seed, err := os.ReadFile("seed_dev.sql")
	require.NoError(t, err)

	tx, err := store.pool.Begin(ctx)
	require.NoError(t, err)
	defer rollbackTx(tx)
	for run := 1; run <= 2; run++ {
		_, err := tx.Exec(ctx, string(seed))
		require.NoError(t, err, "run %d of the seed", run)
	}

	rows, err := tx.Query(ctx, `SELECT email, role FROM users WHERE email IN ('driver@test.com', 'admin@test.com') ORDER BY email`)
	require.NoError(t, err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var email, role string
		require.NoError(t, rows.Scan(&email, &role))
		got = append(got, email+" "+role)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"admin@test.com admin", "driver@test.com driver"}, got)
}
