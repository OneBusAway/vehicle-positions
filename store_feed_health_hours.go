package main

import (
	"context"
	"fmt"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// ListFeedHealthHours returns the hours kept from first to last, both
// included, oldest first.
func (s *Store) ListFeedHealthHours(ctx context.Context, first, last time.Time) ([]healthHour, error) {
	rows, err := s.queries.ListFeedHealthHours(ctx, db.ListFeedHealthHoursParams{
		FirstHour: pgtype.Timestamptz{Time: first, Valid: true},
		LastHour:  pgtype.Timestamptz{Time: last, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list feed health hours: %w", err)
	}
	hours := make([]healthHour, 0, len(rows))
	for _, row := range rows {
		hours = append(hours, healthHour{
			Hour: row.Hour.Time.UTC(),
			Reports: reportOutcomes{
				Accepted:     uint64(row.ReportsAccepted),
				Invalid:      uint64(row.ReportsInvalid),
				Unauthorized: uint64(row.ReportsUnauthorized),
				RateLimited:  uint64(row.ReportsRateLimited),
				ServerError:  uint64(row.ReportsServerError),
			},
			Feed: feedOutcomes{
				Served:      uint64(row.FeedServed),
				Refused:     uint64(row.FeedRefused),
				Invalid:     uint64(row.FeedInvalid),
				ServerError: uint64(row.FeedServerError),
			},
			Vehicles: int(row.Vehicles),
		})
	}
	return hours, nil
}

// SaveFeedHealthHour keeps hour, replacing a row already kept for it.
func (s *Store) SaveFeedHealthHour(ctx context.Context, hour healthHour) error {
	if err := s.queries.SaveFeedHealthHour(ctx, db.SaveFeedHealthHourParams{
		Hour:                pgtype.Timestamptz{Time: hour.Hour, Valid: true},
		ReportsAccepted:     int64(hour.Reports.Accepted),
		ReportsInvalid:      int64(hour.Reports.Invalid),
		ReportsUnauthorized: int64(hour.Reports.Unauthorized),
		ReportsRateLimited:  int64(hour.Reports.RateLimited),
		ReportsServerError:  int64(hour.Reports.ServerError),
		FeedServed:          int64(hour.Feed.Served),
		FeedRefused:         int64(hour.Feed.Refused),
		FeedInvalid:         int64(hour.Feed.Invalid),
		FeedServerError:     int64(hour.Feed.ServerError),
		Vehicles:            int32(hour.Vehicles),
	}); err != nil {
		return fmt.Errorf("save feed health hour: %w", err)
	}
	return nil
}

// PruneFeedHealthHours deletes the hours that started before cutoff.
func (s *Store) PruneFeedHealthHours(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := s.queries.DeleteFeedHealthHoursBefore(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("prune feed health hours: %w", err)
	}
	return n, nil
}
