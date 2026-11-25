package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OneBusAway/vehicle-positions/db"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatabaseAway(t *testing.T) {
	pg := func(code string) error { return fmt.Errorf("save: %w", &pgconn.PgError{Code: code}) }
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"connection failure", pg("08006"), true},
		{"too many connections", pg("53300"), true},
		{"disk full", pg("53100"), true},
		{"shutting down", pg("57P01"), true},
		{"starting up", pg("57P03"), true},
		{"a query that took too long", pg("57014"), true},
		{"an I/O error", pg("58030"), true},
		{"a deadlock", pg("40P01"), true},
		{"a byte that isn't UTF-8", pg("22021"), false},
		{"a broken constraint", pg("23505"), false},
		{"SQL that is wrong", pg("42703"), false},
		{"out of time", fmt.Errorf("acquire: %w", context.DeadlineExceeded), true},
		{"a connection cut off", fmt.Errorf("read: %w", io.ErrUnexpectedEOF), true},
		{"a connection closed", fmt.Errorf("read: %w", io.EOF), true},
		{"any other error", errors.New("bad id"), false},
		{"none", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, databaseAway(tt.err))
		})
	}
}

// newAwayStore is a store whose database is not there: nothing listens on port
// 1. A pool connects only when it is first asked for something.
func newAwayStore(t *testing.T) *Store {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=2")
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return &Store{pool: pool, queries: db.New(pool)}
}

// TestReport_DatabaseAway: a report while the database is away is answered 503,
// which both driver apps keep, at each place that asks it something. They drop
// one answered 500.
func TestReport_DatabaseAway(t *testing.T) {
	token, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: roleDriver}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)
	report := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/locations", strings.NewReader(
			`{"vehicle_id":"bus-1","latitude":1,"longitude":2,"timestamp":`+strconv.FormatInt(time.Now().Unix(), 10)+`}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		return req
	}
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	serve := func(store LocationStore, checker TokenChecker) int {
		limiter := NewVehicleRateLimiter()
		defer limiter.Stop()
		w := httptest.NewRecorder()
		requireDriverAPI(testSecret, checker)(handlePostLocation(store, tracker, limiter)).ServeHTTP(w, report())
		return w.Code
	}
	away, live := newAwayStore(t), newFakeRevocations()

	assert.Equal(t, http.StatusServiceUnavailable, serve(away, away), "the sign-in can't be checked")
	assert.Equal(t, http.StatusServiceUnavailable, serve(away, live), "the driver can't be authorized")
	assert.Equal(t, http.StatusServiceUnavailable, serve(saveAway{away}, live), "the report can't be saved")
	assert.Equal(t, http.StatusInternalServerError, serve(saveFails{}, live), "a report the database refuses")
}

// saveAway authorizes every report and saves none, as its database is away.
type saveAway struct{ *Store }

func (saveAway) AuthorizeLocation(context.Context, *LocationReport) error { return nil }

// saveFails authorizes every report and has each refused.
type saveFails struct{}

func (saveFails) AuthorizeLocation(context.Context, *LocationReport) error { return nil }
func (saveFails) SaveLocation(context.Context, *LocationReport) error {
	return &pgconn.PgError{Code: "23514"}
}

// TestDatabaseAway_FromPostgres: the errors a real server gives, through the
// pool, when it ends a connection, gives up on a query, or refuses one.
func TestDatabaseAway_FromPostgres(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx, "SELECT pg_terminate_backend(pg_backend_pid())")
	require.Error(t, err)
	assert.True(t, databaseAway(err), "a connection the server ended: %v", err)

	conn := dedicatedConn(t)
	_, err = conn.Exec(ctx, "SET statement_timeout = '10ms'")
	require.NoError(t, err)
	_, err = conn.Exec(ctx, "SELECT pg_sleep(5)")
	require.Error(t, err)
	assert.True(t, databaseAway(err), "a query that took too long: %v", err)

	_, err = store.pool.Exec(ctx, "SELECT 1/0")
	require.Error(t, err)
	assert.False(t, databaseAway(err), "a query it refuses: %v", err)
}
