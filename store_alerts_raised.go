package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// AlertsRaisedLister reads the alerts raised in a period, which the Analytics
// page counts.
type AlertsRaisedLister interface {
	ListAlertsRaisedBetween(ctx context.Context, from, to time.Time) ([]raisedAlert, error)
}

// raisedAlert is an alert as the Analytics page counts it. ClearedAt is nil
// while the alert is open.
type raisedAlert struct {
	Kind      string
	RouteID   string
	RaisedAt  time.Time
	ClearedAt *time.Time
}

// listAlertsRaisedBetweenSQL reads the alerts raised in [$1, $2) through
// idx_alert_history_raised, so it reads the period's rows and no others.
const listAlertsRaisedBetweenSQL = `
	SELECT kind, route_id, raised_at, cleared_at
	FROM alert_history
	WHERE raised_at >= $1 AND raised_at < $2
	ORDER BY raised_at, id`

// ListAlertsRaisedBetween returns the alerts raised in [from, to), open or
// cleared, oldest first. It is hand-written like ListTripsStartedBetween,
// which reads the trips of a period the same way.
func (s *Store) ListAlertsRaisedBetween(ctx context.Context, from, to time.Time) ([]raisedAlert, error) {
	rows, err := s.pool.Query(ctx, listAlertsRaisedBetweenSQL, from, to)
	if err != nil {
		return nil, fmt.Errorf("list alerts raised between: %w", err)
	}
	defer rows.Close()

	alerts := make([]raisedAlert, 0)
	for rows.Next() {
		var a raisedAlert
		var cleared pgtype.Timestamptz
		if err := rows.Scan(&a.Kind, &a.RouteID, &a.RaisedAt, &cleared); err != nil {
			return nil, fmt.Errorf("scan alert raised: %w", err)
		}
		if cleared.Valid {
			t := cleared.Time
			a.ClearedAt = &t
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}
