package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/OneBusAway/vehicle-positions/rider"
	"github.com/jackc/pgx/v5/pgtype"
)

// AlertHistoryStore is where the alert recorder keeps each alert's row in
// alert_history.
type AlertHistoryStore interface {
	// RaiseAlert opens a's row and returns its id. An alert that already has
	// an open row keeps it, and the time it was raised.
	RaiseAlert(ctx context.Context, a alertEntry) (int64, error)
	// ClearAlert records that the alert of row id cleared at at.
	ClearAlert(ctx context.Context, id int64, at time.Time) error
	// ClearAlertsLeftOpen records every open alert but the rows in keep as
	// cleared at at, and returns how many it cleared.
	ClearAlertsLeftOpen(ctx context.Context, keep []int64, at time.Time) (int64, error)
}

// ClearedAlertLister reads the cleared alerts the Alerts page lists.
type ClearedAlertLister interface {
	// ListClearedAlerts lists the cleared alerts of kind, or of every kind
	// for "", newest cleared first.
	ListClearedAlerts(ctx context.Context, kind string, limit, offset int) ([]clearedAlert, error)
}

// clearedAlert is one alert that has cleared, as alert_history has it.
type clearedAlert struct {
	ID           int64
	Kind         string
	VehicleID    string
	VehicleLabel string
	// TripID is the trip running, or for a not-started alert the vehicle's
	// last. GtfsTripID and RouteID are the trip not started for that kind,
	// the trip running for the others.
	TripID     int64
	GtfsTripID string
	RouteID    string
	RaisedAt   time.Time
	ClearedAt  time.Time
}

// alertRecorder records each alert in alert_history when it is raised and
// when it clears, whether or not the webhook is on. Like the webhook, it works
// the alerts out every interval and compares them with the ones it has open,
// and a check that fails changes nothing. An alert is raised at its since.
//
// After a restart the watcher has seen nothing yet: it finds a quiet vehicle
// again ALERT_QUIET_AFTER after the start, one not moving once it has had
// ALERT_STOPPED_AFTER of new reports, one bunched once it has been found so
// over ALERT_BUNCHED_AFTER of new reports, and one off route only once it has
// come back near its route and left it. So the alerts the last run left open are
// kept open until settle after the start. One that is listed again by then
// keeps its row, and the others are cleared at the start: each went while the
// server was down, or the watcher cannot see it again, and the start is the
// most the server can say of when.
type alertRecorder struct {
	store    AlertHistoryStore
	source   alertSource
	interval time.Duration
	started  time.Time
	settle   time.Duration

	// open is each alert raised and not cleared. failedRaises is each alert
	// whose raise failed and has not gone through since: it may have opened
	// its row all the same. settled is set once the rows the last run left
	// open are dealt with. Only the checking goroutine uses them.
	open         map[alertKey]openAlert
	failedRaises map[alertKey]alertEntry
	settled      bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

// openAlert is the row of an alert raised and not cleared.
type openAlert struct {
	id int64
	// clearFailed is set when a clear of the row failed. It may have
	// committed all the same, so the alert listed again is raised again: that
	// gives back the row if it is still open, and opens a new one if not.
	clearFailed bool
}

// startAlertJobs starts what follows the alerts the watcher works out: the
// recorder, always, and the webhook, which posts them to cfg's URL, when cfg
// is set. Both work the alerts out every timing.interval, so an alert is
// recorded as raised and cleared when the webhook posts it. stop stops both.
func startAlertJobs(store AlertHistoryStore, alerts *alertWatcher, index func() *rider.Index, cfg *alertWebhookConfig, timing alertWebhookTiming) (stop func()) {
	recorder := newAlertRecorder(store, alerts, alerts.watchingSince, alertHistorySettle(alerts.settings), timing.interval)
	if cfg == nil {
		return recorder.Stop
	}
	webhook := newAlertWebhook(*cfg, alerts, alerts.settings, index, timing)
	alerts.webhook = webhook
	return func() {
		webhook.Stop()
		recorder.Stop()
	}
}

// alertHistorySettle is how long after a start the watcher, judging by
// settings, has seen every alert open at the start again: the longest of the
// thresholds, and a minute more for the reports that show it, which come
// about every 10 seconds, and the check that finds it.
func alertHistorySettle(settings alertSettings) time.Duration {
	return max(settings.quietAfter, settings.stoppedAfter, settings.offRouteAfter, settings.bunchedAfter) + time.Minute
}

// newAlertRecorder starts the recorder: alerts are read from source every
// interval and kept in store. started is when the watcher started.
func newAlertRecorder(store AlertHistoryStore, source alertSource, started time.Time, settle, interval time.Duration) *alertRecorder {
	// A background job outlives any request, so it owns its context.
	ctx, cancel := context.WithCancel(context.Background())
	r := &alertRecorder{
		store:        store,
		source:       source,
		interval:     interval,
		started:      started,
		settle:       settle,
		open:         make(map[alertKey]openAlert),
		failedRaises: make(map[alertKey]alertEntry),
		ctx:          ctx,
		cancel:       cancel,
	}
	r.wg.Go(r.checkEvery)
	return r
}

// Stop cancels a check in flight and waits for the goroutine to exit. The
// alerts open stay open in alert_history, for the next start to find. Safe to
// call more than once.
func (r *alertRecorder) Stop() {
	r.once.Do(r.cancel)
	r.wg.Wait()
}

func (r *alertRecorder) checkEvery() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.check(time.Now())
		case <-r.ctx.Done():
			return
		}
	}
}

// check works the alerts out at now, records as cleared at now each alert
// open that is not listed, and raises each listed that is not open. A write
// that fails is tried again at the next check, a raise even when its alert
// has gone by then.
func (r *alertRecorder) check(now time.Time) {
	ctx, cancel := context.WithTimeout(r.ctx, r.interval)
	defer cancel()
	alerts, err := r.source.list(ctx, now)
	if err != nil {
		if r.ctx.Err() == nil {
			slog.Error("alert history: list alerts", "error", err)
		}
		return
	}

	current := make(map[alertKey]struct{}, len(alerts))
	for _, a := range alerts {
		current[keyOf(a)] = struct{}{}
	}
	// An alert whose raise failed and that has gone since is raised now, for
	// the clears below to record when it went. The raise gives back the row
	// the failed one opened, or opens one if it opened none.
	for key, a := range r.failedRaises {
		if _, ok := current[key]; ok {
			continue
		}
		id, err := r.store.RaiseAlert(ctx, a)
		if err != nil {
			slog.Error("alert history: raise alert", "kind", a.Kind, "trip_id", a.Trip.ID, "error", err)
			continue
		}
		delete(r.failedRaises, key)
		r.open[key] = openAlert{id: id}
	}
	for key, open := range r.open {
		if _, ok := current[key]; ok {
			continue
		}
		if err := r.store.ClearAlert(ctx, open.id, now); err != nil {
			slog.Error("alert history: clear alert", "kind", key.kind, "trip_id", key.tripID, "error", err)
			open.clearFailed = true
			r.open[key] = open
			continue
		}
		delete(r.open, key)
	}
	for _, a := range alerts {
		key := keyOf(a)
		if open, ok := r.open[key]; ok && !open.clearFailed {
			continue
		}
		id, err := r.store.RaiseAlert(ctx, a)
		if err != nil {
			slog.Error("alert history: raise alert", "kind", a.Kind, "trip_id", a.Trip.ID, "error", err)
			r.failedRaises[key] = a
			continue
		}
		delete(r.failedRaises, key)
		r.open[key] = openAlert{id: id}
	}

	// A raise that failed may have opened its row all the same, so the rows
	// left open wait until every failed raise has gone through.
	if r.settled || len(r.failedRaises) > 0 || now.Before(r.started.Add(r.settle)) {
		return
	}
	// Not nil: an empty keep has to clear every row left open.
	keep := make([]int64, 0, len(r.open))
	for _, open := range r.open {
		keep = append(keep, open.id)
	}
	cleared, err := r.store.ClearAlertsLeftOpen(ctx, keep, r.started)
	if err != nil {
		slog.Error("alert history: clear the alerts left open", "error", err)
		return
	}
	r.settled = true
	if cleared > 0 {
		slog.Info("alert history: cleared the alerts open before the start that did not come back", "cleared", cleared)
	}
}

// RaiseAlert opens a's row: the route and GTFS trip are the trip not started
// for a not-started alert, the trip running for the others.
func (s *Store) RaiseAlert(ctx context.Context, a alertEntry) (int64, error) {
	gtfsTripID, routeID := a.Trip.GtfsTripID, a.Trip.RouteID
	if a.NextTrip != nil {
		gtfsTripID, routeID = a.NextTrip.GtfsTripID, a.NextTrip.RouteID
	}
	id, err := s.queries.RaiseAlert(ctx, db.RaiseAlertParams{
		Kind:       a.Kind,
		VehicleID:  a.Trip.VehicleID,
		TripID:     a.Trip.ID,
		GtfsTripID: gtfsTripID,
		RouteID:    routeID,
		RaisedAt:   pgtype.Timestamptz{Time: a.Since, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("raise alert: %w", err)
	}
	return id, nil
}

func (s *Store) ClearAlert(ctx context.Context, id int64, at time.Time) error {
	if err := s.queries.ClearAlert(ctx, db.ClearAlertParams{ID: id, ClearedAt: pgtype.Timestamptz{Time: at, Valid: true}}); err != nil {
		return fmt.Errorf("clear alert: %w", err)
	}
	return nil
}

func (s *Store) ClearAlertsLeftOpen(ctx context.Context, keep []int64, at time.Time) (int64, error) {
	n, err := s.queries.ClearAlertsLeftOpen(ctx, db.ClearAlertsLeftOpenParams{Keep: keep, ClearedAt: pgtype.Timestamptz{Time: at, Valid: true}})
	if err != nil {
		return 0, fmt.Errorf("clear alerts left open: %w", err)
	}
	return n, nil
}

func (s *Store) ListClearedAlerts(ctx context.Context, kind string, limit, offset int) ([]clearedAlert, error) {
	var rows []db.ListClearedAlertsRow
	var err error
	if kind == "" {
		rows, err = s.queries.ListClearedAlerts(ctx, db.ListClearedAlertsParams{Limit: int32(limit), Offset: int32(offset)})
	} else {
		var ofKind []db.ListClearedAlertsOfKindRow
		ofKind, err = s.queries.ListClearedAlertsOfKind(ctx, db.ListClearedAlertsOfKindParams{Kind: kind, Limit: int32(limit), Offset: int32(offset)})
		for _, row := range ofKind {
			rows = append(rows, db.ListClearedAlertsRow(row))
		}
	}
	if err != nil {
		return nil, fmt.Errorf("list cleared alerts: %w", err)
	}

	alerts := make([]clearedAlert, 0, len(rows))
	for _, row := range rows {
		alerts = append(alerts, clearedAlert{
			ID:           row.ID,
			Kind:         row.Kind,
			VehicleID:    row.VehicleID,
			VehicleLabel: row.VehicleLabel,
			TripID:       row.TripID,
			GtfsTripID:   row.GtfsTripID,
			RouteID:      row.RouteID,
			RaisedAt:     row.RaisedAt.Time,
			ClearedAt:    row.ClearedAt.Time,
		})
	}
	return alerts, nil
}

// PruneClearedAlerts deletes the alerts that cleared before cutoff and
// returns how many it deleted.
func (s *Store) PruneClearedAlerts(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := s.queries.DeleteClearedAlertsBefore(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("prune cleared alerts: %w", err)
	}
	return n, nil
}
