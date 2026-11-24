package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// The hours the Feed health page and GET /api/v1/admin/health list: the last
// day, or with ?days=7 the last week.
const (
	healthDayHours  = 24
	healthWeekHours = 7 * healthDayHours
)

// healthHourSaveTimeout bounds the save of the hours left at shutdown, so a
// database that has gone away cannot hold the server's exit.
const healthHourSaveTimeout = 5 * time.Second

// healthHour is one hour's counts, keyed by the hour's start: a row of
// feed_health_hours.
type healthHour struct {
	Hour    time.Time      `json:"hour"`
	Reports reportOutcomes `json:"reports"`
	Feed    feedOutcomes   `json:"feed"`
	// Vehicles is how many vehicles had a live report accepted in the hour,
	// each counted once.
	Vehicles int `json:"vehicles"`
}

// healthHourStore keeps the closed hours.
type healthHourStore interface {
	ListFeedHealthHours(ctx context.Context, first, last time.Time) ([]healthHour, error)
	SaveFeedHealthHour(ctx context.Context, hour healthHour) error
}

var _ healthHourStore = (*Store)(nil)

func unixHour(t time.Time) int64 { return t.Unix() / 3600 }

func hourStart(hour int64) time.Time { return time.Unix(hour*3600, 0).UTC() }

// hourTally is one hour's counts in memory.
type hourTally struct {
	hour   int64
	counts healthCounts
	// vehicles counts each vehicle once, from the first live report this
	// server accepted from it in the hour. vehiclesBefore is the count saved
	// by the server before a restart in the hour: the two may name the same
	// vehicles, so the hour has the larger, not the sum.
	vehicles       int
	vehiclesBefore int
}

func (t hourTally) row() healthHour {
	p := newHealthPeriod(t.counts)
	return healthHour{Hour: hourStart(t.hour), Reports: p.Reports, Feed: p.Feed, Vehicles: max(t.vehicles, t.vehiclesBefore)}
}

// countsOf is the inverse of newHealthPeriod. The feed's rate-limited fetches
// were added to its invalid ones, so they come back as invalid, which reads
// the same.
func countsOf(row healthHour) healthCounts {
	var c healthCounts
	r, f := &c[healthReports], &c[healthFeed]
	r[outcomeOK], r[outcomeInvalid], r[outcomeDenied] = row.Reports.Accepted, row.Reports.Invalid, row.Reports.Unauthorized
	r[outcomeRateLimited], r[outcomeServerError] = row.Reports.RateLimited, row.Reports.ServerError
	f[outcomeOK], f[outcomeDenied], f[outcomeInvalid], f[outcomeServerError] = row.Feed.Served, row.Feed.Refused, row.Feed.Invalid, row.Feed.ServerError
	return c
}

// tally is hour's tally, closing the hour in progress first when hour is
// later. A time earlier than the hour in progress, from a clock stepped back,
// counts in the hour in progress, so an hour is never opened twice. The
// caller holds h.mu.
func (h *feedHealth) tally(hour int64) *hourTally {
	if hour > h.hour.hour {
		// Bounded however long the store is away: the oldest hour waiting
		// gives way, as the minute ring's oldest minute does.
		if over := len(h.closed) - healthWeekHours + 1; over > 0 {
			slog.Warn("feed health hours dropped before they were saved", "hours", over, "oldest", hourStart(h.closed[0].hour))
			h.closed = h.closed[over:]
		}
		h.closed = append(h.closed, h.hour)
		h.hour = hourTally{hour: hour}
	}
	return &h.hour
}

// restoreHours keeps the closed hours in store from now on, and carries on
// the hour in progress from its row, when a server stopped in this hour saved
// one. On an error the hour starts from what this server counts, and the row
// is replaced when the hour closes.
func (h *feedHealth) restoreHours(ctx context.Context, store healthHourStore) error {
	h.mu.Lock()
	h.store = store
	current := h.tally(unixHour(h.now())).hour
	h.mu.Unlock()

	rows, err := store.ListFeedHealthHours(ctx, hourStart(current), hourStart(current))
	if err != nil {
		return fmt.Errorf("read the feed health hour in progress: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// The hour may have closed while the row was read, and be waiting in
	// closed.
	carryOn := func(t *hourTally) {
		if t.hour == current {
			t.counts.add(countsOf(rows[0]))
			t.vehiclesBefore = rows[0].Vehicles
		}
	}
	carryOn(&h.hour)
	for i := range h.closed {
		carryOn(&h.closed[i])
	}
	return nil
}

// saveClosedHours saves the hours that have closed, closing the hour in
// progress first when the clock has left it. Each stays in closed, where
// hourly reads it, until the store has it. An hour the store refuses waits for
// the next call, with the ones after it.
func (h *feedHealth) saveClosedHours(ctx context.Context) {
	h.saving.Lock()
	defer h.saving.Unlock()
	for {
		h.mu.Lock()
		h.tally(unixHour(h.now()))
		store, waiting := h.store, len(h.closed)
		if store == nil || waiting == 0 {
			h.mu.Unlock()
			return
		}
		t := h.closed[0]
		h.mu.Unlock()

		if err := store.SaveFeedHealthHour(ctx, t.row()); err != nil {
			slog.Error("feed health hour save failed", "error", err, "hour", hourStart(t.hour), "waiting", waiting)
			return
		}
		h.mu.Lock()
		// It gave way to a newer hour if one closed during the save with no
		// room left.
		if len(h.closed) > 0 && h.closed[0].hour == t.hour {
			h.closed = h.closed[1:]
		}
		h.mu.Unlock()
	}
}

// saveHourInProgress saves the hour in progress as it stands, for a server
// started later in the hour to carry on from. Only a stopping server calls it.
func (h *feedHealth) saveHourInProgress(ctx context.Context) {
	h.mu.Lock()
	t, store := h.hour, h.store
	h.mu.Unlock()
	if err := store.SaveFeedHealthHour(ctx, t.row()); err != nil {
		slog.Error("feed health hour in progress not saved at shutdown", "error", err, "hour", hourStart(t.hour))
	}
}

// hourly is the last days' hours, oldest first, ending with the hour in
// progress, with every hour listed. The hours still in memory, the one in
// progress and any closed and not saved yet, are read there; the rest from
// the store.
func (h *feedHealth) hourly(ctx context.Context, days int) ([]healthHour, error) {
	h.mu.Lock()
	current := h.tally(unixHour(h.now())).hour
	inMemory := make(map[int64]healthHour, len(h.closed)+1)
	for _, t := range h.closed {
		inMemory[t.hour] = t.row()
	}
	inMemory[current] = h.hour.row()
	store := h.store
	h.mu.Unlock()

	first := current - int64(days*healthDayHours) + 1
	if store != nil {
		rows, err := store.ListFeedHealthHours(ctx, hourStart(first), hourStart(current))
		if err != nil {
			return nil, fmt.Errorf("list feed health hours: %w", err)
		}
		for _, row := range rows {
			if _, ok := inMemory[unixHour(row.Hour)]; !ok {
				inMemory[unixHour(row.Hour)] = row
			}
		}
	}
	hours := make([]healthHour, 0, current-first+1)
	for hour := first; hour <= current; hour++ {
		row, ok := inMemory[hour]
		if !ok {
			row = healthHour{Hour: hourStart(hour)}
		}
		hours = append(hours, row)
	}
	return hours, nil
}

// report is the snapshot with the last days' hours.
func (h *feedHealth) report(ctx context.Context, days int) feedHealthResponse {
	resp := h.snapshot()
	hours, err := h.hourly(ctx, days)
	if err != nil {
		slog.Error("feed health hours unreadable", "error", err)
		resp.HoursError = "the hourly counts could not be read from the database"
		return resp
	}
	resp.Hours = hours
	return resp
}

// parseHealthDays reads ?days, 1 when it is not given; ok is false for any
// value but 1 and 7.
func parseHealthDays(r *http.Request) (days int, ok bool) {
	switch r.URL.Query().Get("days") {
	case "", "1":
		return 1, true
	case "7":
		return 7, true
	}
	return 0, false
}

// healthHourWriter saves each hour of h as it closes, checking every interval,
// and the hour in progress when it stops.
type healthHourWriter struct {
	health   *feedHealth
	interval time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

// startHealthHours has h keep its hours in store: it carries on the hour in
// progress from store, then starts saving each hour as it closes.
func startHealthHours(ctx context.Context, h *feedHealth, store healthHourStore, interval time.Duration) *healthHourWriter {
	if err := h.restoreHours(ctx, store); err != nil {
		slog.Warn("feed health hour in progress not carried on", "error", err)
	}
	// A background job outlives any request, so it owns its context.
	jobCtx, cancel := context.WithCancel(context.Background())
	w := &healthHourWriter{health: h, interval: interval, ctx: jobCtx, cancel: cancel}
	w.wg.Go(w.run)
	return w
}

func (w *healthHourWriter) run() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.health.saveClosedHours(w.ctx)
		case <-w.ctx.Done():
			return
		}
	}
}

// Stop ends the checks, then saves the hours closed and the one in progress,
// so a restart keeps them. The server has stopped taking requests by then, so
// nothing more is counted in the hour. Safe to call more than once.
func (w *healthHourWriter) Stop() {
	w.once.Do(func() {
		w.cancel()
		w.wg.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), healthHourSaveTimeout)
		defer cancel()
		w.health.saveClosedHours(ctx)
		w.health.saveHourInProgress(ctx)
	})
}
