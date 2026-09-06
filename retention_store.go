package main

import (
	"context"
	"fmt"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// LocationPruneStore is the store behavior needed by the retention pruner.
type LocationPruneStore interface {
	PruneLocationPoints(ctx context.Context, cutoff time.Time, batchSize int32) (int64, error)
}

var _ LocationPruneStore = (*Store)(nil)

// PruneLocationPoints deletes up to batchSize location points received before
// cutoff and returns the number of rows removed.
//
// The cutoff is compared against received_at (server-assigned) rather than
// timestamp (client-supplied), so a device reporting a wrong clock can neither
// evade retention nor delete its own history early.
func (s *Store) PruneLocationPoints(ctx context.Context, cutoff time.Time, batchSize int32) (int64, error) {
	rows, err := s.queries.DeleteLocationPointsBefore(ctx, db.DeleteLocationPointsBeforeParams{
		Cutoff:    pgtype.Timestamptz{Time: cutoff, Valid: true},
		BatchSize: batchSize,
	})
	if err != nil {
		return 0, fmt.Errorf("prune location points: %w", err)
	}
	return rows, nil
}
