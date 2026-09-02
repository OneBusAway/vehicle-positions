package main

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registerTestRider(t *testing.T, store *Store) *Rider {
	t.Helper()
	r, created, err := store.RegisterRider(context.Background(), uuid.NewString(), "ios", "org.test", "1.0")
	require.NoError(t, err)
	require.True(t, created)
	return r
}

func startTestRide(t *testing.T, store *Store, riderID string) *Ride {
	t.Helper()
	ride := &Ride{ID: uuid.NewString(), RiderID: riderID, TripID: "T1", StartDate: "20260902", RouteID: "R1"}
	require.NoError(t, store.StartRide(context.Background(), ride))
	return ride
}

func TestRiderStore_RegisterIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	inst := uuid.NewString()
	r1, created, err := store.RegisterRider(context.Background(), inst, "ios", "org.test", "1.0")
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "new", r1.Tier)
	assert.Equal(t, 0, r1.Score)

	r2, created, err := store.RegisterRider(context.Background(), inst, "ios", "org.test", "1.1")
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, r1.ID, r2.ID)
	assert.Equal(t, "1.1", r2.AppVersion)

	got, err := store.GetRider(context.Background(), r1.ID)
	require.NoError(t, err)
	assert.Equal(t, inst, got.InstallationID)
	_, err = store.GetRider(context.Background(), uuid.NewString())
	assert.ErrorIs(t, err, ErrRiderNotFound)
}

func TestRiderStore_StartRideAndRecordPoints(t *testing.T) {
	store := newTestStore(t)
	r := registerTestRider(t, store)
	ride := startTestRide(t, store, r.ID)
	assert.False(t, ride.StartedAt.IsZero())
	assert.Equal(t, "active", ride.Status)
	assert.Equal(t, "pending", ride.State)

	acc := 5.0
	pts := []RidePointRecord{
		{Latitude: 47.6, Longitude: -122.33, Accuracy: &acc, Timestamp: 1756800000, Outcome: "matched", Corroboration: "unavailable", AlongShape: 10, DistanceToShape: 1},
		{Latitude: 47.601, Longitude: -122.33, Timestamp: 1756800005, Outcome: "matched", Corroboration: "none", AlongShape: 20, DistanceToShape: 2, ScheduleDeviationSeconds: 30},
	}
	prog := RideProgress{State: "verified", Corroborated: false, PointsTotal: 2, PointsMatched: 2}
	require.NoError(t, store.RecordRidePoints(context.Background(), ride.ID, r.ID, pts, prog))

	n, err := store.queries.CountRidePointsForRide(context.Background(), ride.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)
	got, err := store.queries.GetRide(context.Background(), ride.ID)
	require.NoError(t, err)
	assert.Equal(t, "verified", got.State)
	assert.EqualValues(t, 2, got.PointsMatched)

	rider, err := store.GetRider(context.Background(), r.ID)
	require.NoError(t, err)
	assert.True(t, rider.LastSeenAt.After(r.LastSeenAt), "recording points touches the rider")
}

func TestRiderStore_FinishRide_UpdatesScoreAndTier(t *testing.T) {
	store := newTestStore(t)
	r := registerTestRider(t, store)
	for i := 0; i < 3; i++ {
		ride := startTestRide(t, store, r.ID)
		updated, err := store.FinishRide(context.Background(), ride.ID, RideOutcome{
			EndReason: "arrived", ScoreDelta: 1, Corroborated: true,
			Progress: RideProgress{State: "verified", Corroborated: true, PointsTotal: 50, PointsMatched: 50, PointsCorroborated: 40},
		})
		require.NoError(t, err)
		assert.Equal(t, i+1, updated.Score)
		assert.Equal(t, i+1, updated.RidesTotal)
	}
	got, _ := store.GetRider(context.Background(), r.ID)
	assert.Equal(t, "trusted", got.Tier)
	assert.Equal(t, 3, got.RidesCorroborated)

	ride := startTestRide(t, store, r.ID)
	updated, err := store.FinishRide(context.Background(), ride.ID, RideOutcome{EndReason: "contradicted", ScoreDelta: -3, Rejected: true, Progress: RideProgress{State: "rejected"}})
	require.NoError(t, err)
	assert.Equal(t, 0, updated.Score)
	assert.Equal(t, "new", updated.Tier)
	assert.Equal(t, 1, updated.RidesRejected)

	// ListRides is not scoped to a rider and the table outlives the test run,
	// so assert on the newest rides rather than an exact count.
	rides, err := store.ListRides(context.Background(), "ended", 10, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rides), 4)
	assert.Equal(t, "contradicted", rides[0].EndReason, "newest first")
	assert.NotNil(t, rides[0].EndedAt)

	_, err = store.FinishRide(context.Background(), ride.ID, RideOutcome{EndReason: "arrived"})
	assert.ErrorIs(t, err, ErrRideNotFound, "already ended")
}

func TestRiderStore_ScoreClamps(t *testing.T) {
	store := newTestStore(t)
	r := registerTestRider(t, store)
	var last *Rider
	for i := 0; i < 5; i++ {
		ride := startTestRide(t, store, r.ID)
		var err error
		// Progress carries the ride's final state, which the rides_state_check
		// constraint requires to be one of the three known states.
		last, err = store.FinishRide(context.Background(), ride.ID, RideOutcome{
			EndReason: "contradicted", ScoreDelta: -3, Rejected: true,
			Progress: RideProgress{State: "rejected"},
		})
		require.NoError(t, err)
	}
	assert.Equal(t, -10, last.Score)
	assert.Equal(t, "blocked", last.Tier)
}

func TestRiderStore_EndAllActiveRides_AndCascade(t *testing.T) {
	store := newTestStore(t)
	r := registerTestRider(t, store)
	a := startTestRide(t, store, r.ID)
	b := startTestRide(t, store, r.ID)
	// Recorded while a is still active; the cascade assertion below needs a
	// point row to exist in the first place.
	require.NoError(t, store.RecordRidePoints(context.Background(), a.ID, r.ID,
		[]RidePointRecord{{Latitude: 1, Longitude: 1, Timestamp: 1, Outcome: "matched", Corroboration: "none"}},
		RideProgress{State: "pending", PointsTotal: 1, PointsMatched: 1}))
	before, err := store.queries.CountRidePointsForRide(context.Background(), a.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, before)

	n, err := store.EndAllActiveRides(context.Background(), "server_restart")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(2))
	ga, _ := store.queries.GetRide(context.Background(), a.ID)
	gb, _ := store.queries.GetRide(context.Background(), b.ID)
	assert.Equal(t, "server_restart", ga.EndReason)
	assert.Equal(t, "ended", gb.Status)
	active, err := store.ListRides(context.Background(), "active", 200, 0)
	require.NoError(t, err)
	for _, ride := range active {
		assert.NotEqual(t, r.ID, ride.RiderID, "this rider has no active rides left")
	}

	tiers, err := store.CountRidersByTier(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, tiers["new"], 1)

	// Deleting the rider cascades to rides and points.
	_, err = store.pool.Exec(context.Background(), "DELETE FROM riders WHERE id = $1", r.ID)
	require.NoError(t, err)
	_, err = store.queries.GetRide(context.Background(), a.ID)
	assert.ErrorIs(t, err, pgx.ErrNoRows)
	points, err := store.queries.CountRidePointsForRide(context.Background(), a.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, points, "points go with the ride")
}

func TestRiderStore_DeleteRidePointsBefore(t *testing.T) {
	store := newTestStore(t)
	r := registerTestRider(t, store)
	ride := startTestRide(t, store, r.ID)
	require.NoError(t, store.RecordRidePoints(context.Background(), ride.ID, r.ID,
		[]RidePointRecord{{Latitude: 1, Longitude: 1, Timestamp: 1, Outcome: "matched", Corroboration: "none"}}, RideProgress{State: "pending", PointsTotal: 1}))

	_, err := store.DeleteRidePointsBefore(context.Background(), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	n, err := store.queries.CountRidePointsForRide(context.Background(), ride.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n, "a point received just now survives an hour-old cutoff")

	deleted, err := store.DeleteRidePointsBefore(context.Background(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, deleted, int64(1))
	n, err = store.queries.CountRidePointsForRide(context.Background(), ride.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, n, "a cutoff in the future prunes the point")
}

func TestRiderStore_RecordRidePointsDoesNotResurrectEndedRide(t *testing.T) {
	store := newTestStore(t)
	r := registerTestRider(t, store)
	ride := startTestRide(t, store, r.ID)

	_, err := store.FinishRide(context.Background(), ride.ID, RideOutcome{
		EndReason: "arrived", ScoreDelta: 1,
		Progress: RideProgress{State: "verified", Corroborated: true, PointsTotal: 3, PointsMatched: 3, PointsCorroborated: 2},
	})
	require.NoError(t, err)

	// A batch that was in flight when the ride ended, arriving late. Neither the
	// ride's counters nor its point history may be rewritten.
	require.NoError(t, store.RecordRidePoints(context.Background(), ride.ID, r.ID,
		[]RidePointRecord{{Latitude: 47.6, Longitude: -122.33, Timestamp: 1756800000, Outcome: "matched", Corroboration: "none"}},
		RideProgress{State: "pending", PointsTotal: 99, PointsMatched: 99}))

	got, err := store.queries.GetRide(context.Background(), ride.ID)
	require.NoError(t, err)
	assert.Equal(t, "ended", got.Status)
	assert.Equal(t, "verified", got.State)
	assert.True(t, got.Corroborated)
	assert.EqualValues(t, 3, got.PointsTotal, "the finished ride's counters stand")
	assert.EqualValues(t, 2, got.PointsCorroborated)

	points, err := store.queries.CountRidePointsForRide(context.Background(), ride.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, points, "no points are appended to an ended ride")
}
