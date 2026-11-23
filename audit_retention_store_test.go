package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The retention tests date their rows in 2000, before any row another test
// writes, so a cutoff there deletes only theirs and the counts are exact.

// insertOldAuditRow writes a vehicle's audit row dated at, as no store
// method can: the log dates each row itself.
func insertOldAuditRow(t *testing.T, store *Store, vehicleID string, at time.Time) {
	t.Helper()
	_, err := store.pool.Exec(context.Background(),
		`INSERT INTO audit_log (at, actor_email, actor_ip, object_type, object_id, action, after)
		 VALUES ($1, '', '', 'vehicle', $2, 'create', '{}')`, at, vehicleID)
	require.NoError(t, err)
}

// oldAuditRows lists the vehicles whose rows from 2000 are still there,
// oldest first.
func oldAuditRows(t *testing.T, store *Store) []string {
	t.Helper()
	rows, err := store.pool.Query(context.Background(),
		`SELECT object_id FROM audit_log WHERE object_id LIKE 'aud-%' AND at < '2001-01-01' ORDER BY at`)
	require.NoError(t, err)
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

// TestStore_PruneAuditLog deletes the rows written before the cutoff and
// keeps the rest, one at the cutoff included, with the trigger that refuses
// UPDATE in place: it refuses only UPDATE, so retention can delete.
func TestStore_PruneAuditLog(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := context.Background()

	var triggers int
	require.NoError(t, store.pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_trigger WHERE tgrelid = 'audit_log'::regclass AND tgname = 'audit_log_refuse_update'`).Scan(&triggers))
	require.Equal(t, 1, triggers, "the delete must run against the table as migrated, trigger and all")

	cutoff := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
	insertOldAuditRow(t, store, "aud-older", cutoff.Add(-24*time.Hour))
	insertOldAuditRow(t, store, "aud-at-cutoff", cutoff)
	insertOldAuditRow(t, store, "aud-newer", cutoff.Add(time.Second))

	deleted, err := store.PruneAuditLog(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	assert.Equal(t, []string{"aud-at-cutoff", "aud-newer"}, oldAuditRows(t, store))

	deleted, err = store.PruneAuditLog(ctx, cutoff)
	require.NoError(t, err)
	assert.Zero(t, deleted, "a second pass finds nothing left to delete")
}

// TestAuditLogPruner_AgainstTheDatabase runs the job on the real store: a row
// older than the retention period goes on the next pass, a younger one stays.
func TestAuditLogPruner_AgainstTheDatabase(t *testing.T) {
	store := newAuditTestStore(t)
	cutoff := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
	insertOldAuditRow(t, store, "aud-older", cutoff.Add(-24*time.Hour))
	insertOldAuditRow(t, store, "aud-younger", cutoff.Add(24*time.Hour))

	pruner, err := NewAuditLogPruner(store, time.Since(cutoff), 5*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(pruner.Stop)

	// Eventually polls from another goroutine, where a failed require could
	// not stop the test, so the poll only counts.
	require.Eventually(t, func() bool {
		var left int
		err := store.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM audit_log WHERE object_id LIKE 'aud-%' AND at < '2001-01-01'`).Scan(&left)
		return err == nil && left == 1
	}, 5*time.Second, 10*time.Millisecond, "the row older than the retention period must be deleted")
	pruner.Stop()
	assert.Equal(t, []string{"aud-younger"}, oldAuditRows(t, store))
}
