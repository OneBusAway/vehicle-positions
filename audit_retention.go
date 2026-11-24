package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// auditLogPruneInterval is how often the audit log is swept: hourly, as
// location history is by default. It has no setting of its own: the log
// gains a row for each admin change and each sign-in, failed sign-in and
// sign-out of an admin or a viewer, so an hour either way changes nothing.
const auditLogPruneInterval = time.Hour

// startAuditLogRetention starts deleting the audit log's rows once they are
// older than AUDIT_LOG_RETENTION_PERIOD, and returns nil when every row is
// kept. Keeping them is the default, as it is for location history: unset or
// 0 keeps everything. A value that is not a duration is warned about and
// ignored, as every duration setting's is, and a negative one logs an ERROR;
// either way nothing is deleted, the safe direction for a record of changes.
// The server still starts, as it does on a bad LOCATION_RETENTION_PERIOD.
func startAuditLogRetention(store AuditLogPruneStore) *AuditLogPruner {
	retention := envDurationOrDefault("AUDIT_LOG_RETENTION_PERIOD", 0)
	if retention == 0 {
		return nil
	}
	pruner, err := NewAuditLogPruner(store, retention, auditLogPruneInterval)
	if err != nil {
		slog.Error("audit log retention disabled: invalid configuration", "error", err)
		return nil
	}
	slog.Info("audit log retention enabled",
		"retention", retention.String(), "interval", auditLogPruneInterval.String())
	return pruner
}

// AuditLogPruner periodically deletes the audit log's rows older than the
// retention period.
//
// The lifecycle is LocationPruner's: the worker owns a cancellable context, so
// Stop aborts a delete already running on the database rather than only
// stopping the next one.
type AuditLogPruner struct {
	store     AuditLogPruneStore
	retention time.Duration
	interval  time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// NewAuditLogPruner starts the background pruning goroutine.
//
// Retention and interval must be positive. A non-positive retention would put
// the cutoff at "now" or later and delete the whole log, and a non-positive
// interval would panic time.NewTicker, so both are rejected rather than
// defaulted.
func NewAuditLogPruner(store AuditLogPruneStore, retention, interval time.Duration) (*AuditLogPruner, error) {
	switch {
	case retention <= 0:
		return nil, fmt.Errorf("audit log retention period must be positive, got %s", retention)
	case interval <= 0:
		return nil, fmt.Errorf("audit log prune interval must be positive, got %s", interval)
	}

	// A background job outlives any single request, so it owns its context
	// rather than borrowing a request's.
	ctx, cancel := context.WithCancel(context.Background())
	p := &AuditLogPruner{
		store:     store,
		retention: retention,
		interval:  interval,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
	}

	go p.run()
	return p, nil
}

// Stop cancels any prune already in flight and waits for the goroutine to exit.
// Safe to call more than once.
func (p *AuditLogPruner) Stop() {
	p.once.Do(p.cancel)
	<-p.done
}

func (p *AuditLogPruner) run() {
	defer close(p.done)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.prune(p.ctx)
		case <-p.ctx.Done():
			return
		}
	}
}

// prune deletes every row older than the retention period in one statement.
// Unlike LocationPruner it does not batch: the log gains a few rows a day,
// plus one for each row a CSV import writes, each time a script signs in as an
// admin, and two every failedSignInWindow for each address guessing at an
// account's password, so even the first pass after retention is turned on is a
// short delete.
func (p *AuditLogPruner) prune(ctx context.Context) {
	start := time.Now()
	cutoff := start.Add(-p.retention)

	deleted, err := p.store.PruneAuditLog(ctx, cutoff)
	if err != nil {
		// A delete aborted by shutdown is expected, not a failure worth
		// alarming on.
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return
		}
		slog.Error("audit log retention prune failed", "error", err, "cutoff", cutoff)
		return
	}

	if deleted > 0 {
		slog.Info("pruned expired audit log rows",
			"deleted", deleted,
			"cutoff", cutoff,
			"retention", p.retention.String(),
			"duration_ms", float64(time.Since(start).Microseconds())/1000.0,
		)
	}
}
