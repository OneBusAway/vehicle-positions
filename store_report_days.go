package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// serviceDayStore keeps the totals of the service days the Analytics page has
// added up, once they no longer change.
type serviceDayStore interface {
	ListServiceDays(ctx context.Context, first, last time.Time) ([]storedServiceDay, error)
	SaveServiceDay(ctx context.Context, day storedServiceDay) error
}

var _ serviceDayStore = (*Store)(nil)

// storedServiceDay is one row of service_day_totals: a day's totals and the
// on-time window they were judged by.
type storedServiceDay struct {
	// Date is the service date, midnight UTC.
	Date        time.Time
	OnTimeEarly time.Duration
	OnTimeLate  time.Duration
	Totals      dayTotals
}

// ListServiceDays returns the days kept from first to last, both included,
// oldest first.
func (s *Store) ListServiceDays(ctx context.Context, first, last time.Time) ([]storedServiceDay, error) {
	rows, err := s.queries.ListServiceDayTotals(ctx, db.ListServiceDayTotalsParams{
		FirstDate: pgtype.Date{Time: first, Valid: true},
		LastDate:  pgtype.Date{Time: last, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list service day totals: %w", err)
	}
	days := make([]storedServiceDay, 0, len(rows))
	for _, row := range rows {
		day := storedServiceDay{
			Date:        row.ServiceDate.Time,
			OnTimeEarly: time.Duration(row.OnTimeEarlyS) * time.Second,
			OnTimeLate:  time.Duration(row.OnTimeLateS) * time.Second,
		}
		if err := json.Unmarshal(row.Totals, &day.Totals); err != nil {
			return nil, fmt.Errorf("read service day totals for %s: %w", day.Date.Format(reportDateLayout), err)
		}
		day.Totals.Date = day.Date.Format(reportDateLayout)
		days = append(days, day)
	}
	return days, nil
}

// SaveServiceDay keeps day, replacing a row already kept for its date.
func (s *Store) SaveServiceDay(ctx context.Context, day storedServiceDay) error {
	totals, err := json.Marshal(day.Totals)
	if err != nil {
		return fmt.Errorf("encode service day totals: %w", err)
	}
	if err := s.queries.SaveServiceDayTotals(ctx, db.SaveServiceDayTotalsParams{
		ServiceDate:  pgtype.Date{Time: day.Date, Valid: true},
		OnTimeEarlyS: int32(day.OnTimeEarly / time.Second),
		OnTimeLateS:  int32(day.OnTimeLate / time.Second),
		Totals:       totals,
	}); err != nil {
		return fmt.Errorf("save service day totals: %w", err)
	}
	return nil
}

// PruneServiceDays deletes the days whose service date is before cutoff's.
func (s *Store) PruneServiceDays(ctx context.Context, cutoff time.Time) (int64, error) {
	y, m, d := cutoff.UTC().Date()
	n, err := s.queries.DeleteServiceDayTotalsBefore(ctx, pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true})
	if err != nil {
		return 0, fmt.Errorf("prune service day totals: %w", err)
	}
	return n, nil
}
