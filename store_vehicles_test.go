package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uniqueVehicleID generates a unique test vehicle ID to avoid cross-test collisions.
func uniqueVehicleID(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("veh-%d-%s", time.Now().UnixNano(), sanitizeTestName(t.Name()))
}

// cleanupVehicleID registers cleanup to delete a single test vehicle's dependent
// rows and the vehicle itself, in FK-safe order (dependents before the row itself).
func cleanupVehicleID(t *testing.T, store *Store, id string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, err := store.pool.Exec(ctx, "DELETE FROM location_points WHERE vehicle_id = $1", id)
		require.NoError(t, err)
		_, err = store.pool.Exec(ctx, "DELETE FROM vehicles WHERE id = $1", id)
		require.NoError(t, err)
	})
}

// cleanupVehicles ensures a clean slate before and after each test.
// Pre-test cleanup handles cases where a prior test panicked before its t.Cleanup ran.
func cleanupVehicles(t *testing.T, store *Store) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
		require.NoError(t, err)
		_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
		require.NoError(t, err)
	})
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, "DELETE FROM location_points")
	require.NoError(t, err)
	_, err = store.pool.Exec(ctx, "DELETE FROM vehicles")
	require.NoError(t, err)
}

func TestStore_UpsertVehicle_CreateNew(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)

	v, err := store.UpsertVehicle(context.Background(), "bus-42", "Bus 42", "nairobi")
	require.NoError(t, err)

	assert.Equal(t, "bus-42", v.ID)
	assert.Equal(t, "Bus 42", v.Label)
	assert.Equal(t, "nairobi", v.AgencyTag)
	assert.True(t, v.Active)
	assert.False(t, v.CreatedAt.IsZero())
	assert.False(t, v.UpdatedAt.IsZero())
}

func TestStore_UpsertVehicle_UpdateExisting(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	created, err := store.UpsertVehicle(ctx, "bus-upd", "Old Label", "old-tag")
	require.NoError(t, err)

	updated, err := store.UpsertVehicle(ctx, "bus-upd", "New Label", "new-tag")
	require.NoError(t, err)

	assert.Equal(t, "bus-upd", updated.ID)
	assert.Equal(t, "New Label", updated.Label)
	assert.Equal(t, "new-tag", updated.AgencyTag)
	assert.True(t, updated.Active)
	assert.Equal(t, created.CreatedAt, updated.CreatedAt, "created_at should not change on upsert update")
	assert.True(t, updated.UpdatedAt.After(created.UpdatedAt) || updated.UpdatedAt.Equal(created.UpdatedAt),
		"updated_at should be >= created_at after upsert update")
}

func TestStore_ListVehicles(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "bus-a", "Bus A", "agency-1")
	require.NoError(t, err)
	_, err = store.UpsertVehicle(ctx, "bus-b", "Bus B", "agency-2")
	require.NoError(t, err)

	vehicles, err := store.ListVehicles(ctx)
	require.NoError(t, err)
	assert.Len(t, vehicles, 2)
}

func TestStore_ListVehicles_Empty(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)

	vehicles, err := store.ListVehicles(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, vehicles, "empty list should be [], not nil")
	assert.Empty(t, vehicles)
}

func TestStore_GetVehicle(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "bus-get", "Bus Get", "nairobi")
	require.NoError(t, err)

	v, err := store.GetVehicle(ctx, "bus-get")
	require.NoError(t, err)
	assert.Equal(t, "bus-get", v.ID)
	assert.Equal(t, "Bus Get", v.Label)
	assert.Equal(t, "nairobi", v.AgencyTag)
	assert.True(t, v.Active)
}

func TestStore_GetVehicle_NotFound(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)

	v, err := store.GetVehicle(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, v)
	assert.True(t, errors.Is(err, pgx.ErrNoRows),
		"error must wrap pgx.ErrNoRows so handlers can distinguish not-found from database failures")
}

func TestStore_DeactivateVehicle(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "bus-deact", "Bus Deact", "")
	require.NoError(t, err)

	err = store.DeactivateVehicle(ctx, "bus-deact")
	require.NoError(t, err)

	v, err := store.GetVehicle(ctx, "bus-deact")
	require.NoError(t, err)
	assert.False(t, v.Active, "vehicle should be inactive after deactivation")
}

func TestStore_DeactivateVehicle_NotFound(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)

	err := store.DeactivateVehicle(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, pgx.ErrNoRows),
		"error must wrap pgx.ErrNoRows so handlers can distinguish not-found from database failures")
}

func TestStore_DeactivateVehicle_AlreadyInactive(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "bus-idem", "Bus Idem", "")
	require.NoError(t, err)

	err = store.DeactivateVehicle(ctx, "bus-idem")
	require.NoError(t, err)

	// The SQL matches on id only (not active), so deactivating an already-inactive
	// vehicle still affects one row and succeeds without error.
	err = store.DeactivateVehicle(ctx, "bus-idem")
	require.NoError(t, err, "deactivating an already-inactive vehicle should not error")
}

func TestStore_UpsertVehicle_ReactivatesDeactivatedVehicle(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "bus-react", "Bus React", "agency-1")
	require.NoError(t, err)

	err = store.DeactivateVehicle(ctx, "bus-react")
	require.NoError(t, err)

	v, err := store.GetVehicle(ctx, "bus-react")
	require.NoError(t, err)
	require.False(t, v.Active, "vehicle should be inactive after deactivation")

	// Upserting a deactivated vehicle should reactivate it.
	reactivated, err := store.UpsertVehicle(ctx, "bus-react", "Bus React Updated", "agency-2")
	require.NoError(t, err)
	assert.True(t, reactivated.Active, "upsert should reactivate a deactivated vehicle")
	assert.Equal(t, "Bus React Updated", reactivated.Label)
	assert.Equal(t, "agency-2", reactivated.AgencyTag)
}

func TestStore_UpsertVehicle_RoundTrip(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	created, err := store.UpsertVehicle(ctx, "bus-rt", "Bus RT", "agency-rt")
	require.NoError(t, err)

	fetched, err := store.GetVehicle(ctx, "bus-rt")
	require.NoError(t, err)

	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, created.Label, fetched.Label)
	assert.Equal(t, created.AgencyTag, fetched.AgencyTag)
	assert.Equal(t, created.Active, fetched.Active)
	assert.Equal(t, created.CreatedAt.Unix(), fetched.CreatedAt.Unix(), "created_at should round-trip")
	assert.Equal(t, created.UpdatedAt.Unix(), fetched.UpdatedAt.Unix(), "updated_at should round-trip")
}

func cleanupDriverVehicles(t *testing.T, store *Store) {
	t.Helper()
	clean := func() {
		ctx := context.Background()
		for _, table := range []string{"location_points", "user_vehicles", "trips", "vehicles", "users"} {
			_, err := store.pool.Exec(ctx, "DELETE FROM "+table)
			require.NoError(t, err)
		}
	}
	t.Cleanup(clean)
	clean()
}

func TestStore_ListActiveVehiclesByUser(t *testing.T) {
	store := newTestStore(t)
	cleanupDriverVehicles(t, store)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, "Driver", "driver-lav@example.com", "pw-hash", "driver")
	require.NoError(t, err)

	_, err = store.UpsertVehicle(ctx, "bus-active", "Bus Active", "agency")
	require.NoError(t, err)
	_, err = store.UpsertVehicle(ctx, "bus-inactive", "Bus Inactive", "agency")
	require.NoError(t, err)
	_, err = store.UpsertVehicle(ctx, "bus-unassigned", "Bus Unassigned", "agency")
	require.NoError(t, err)

	_, err = store.CreateAssignment(ctx, user.ID, "bus-active")
	require.NoError(t, err)
	_, err = store.CreateAssignment(ctx, user.ID, "bus-inactive")
	require.NoError(t, err)
	require.NoError(t, store.DeactivateVehicle(ctx, "bus-inactive"))

	vehicles, err := store.ListActiveVehiclesByUser(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, vehicles, 1, "only active, assigned vehicles")
	assert.Equal(t, "bus-active", vehicles[0].ID)
	assert.Equal(t, "Bus Active", vehicles[0].Label)
	assert.True(t, vehicles[0].Active)
}

func TestStore_ListActiveVehiclesByUser_Empty(t *testing.T) {
	store := newTestStore(t)
	cleanupDriverVehicles(t, store)

	vehicles, err := store.ListActiveVehiclesByUser(context.Background(), 999999)
	require.NoError(t, err)
	assert.NotNil(t, vehicles, "empty list should be [], not nil")
	assert.Empty(t, vehicles)
}

func TestUpdateVehicleInfoDoesNotReactivate(t *testing.T) {
	store := newTestStore(t)
	id := uniqueVehicleID(t)
	cleanupVehicleID(t, store, id)
	_, err := store.UpsertVehicle(context.Background(), id, "Old", "tag")
	require.NoError(t, err)
	require.NoError(t, store.DeactivateVehicle(context.Background(), id))

	require.NoError(t, store.UpdateVehicleInfo(context.Background(), id, "New Label", "newtag"))
	v, err := store.GetVehicle(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "New Label", v.Label)
	assert.False(t, v.Active, "editing must not reactivate a deactivated vehicle")

	err = store.UpdateVehicleInfo(context.Background(), "no-such-vehicle-xyz", "x", "y")
	assert.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestSetVehicleActive(t *testing.T) {
	store := newTestStore(t)
	id := uniqueVehicleID(t)
	cleanupVehicleID(t, store, id)
	_, err := store.UpsertVehicle(context.Background(), id, "Bus", "")
	require.NoError(t, err)

	require.NoError(t, store.SetVehicleActive(context.Background(), id, false))
	v, _ := store.GetVehicle(context.Background(), id)
	assert.False(t, v.Active)
	require.NoError(t, store.SetVehicleActive(context.Background(), id, true))
	v, _ = store.GetVehicle(context.Background(), id)
	assert.True(t, v.Active)
	assert.ErrorIs(t, store.SetVehicleActive(context.Background(), "no-such-vehicle-xyz", true), pgx.ErrNoRows)
}

func TestCountActiveVehiclesAndTrips(t *testing.T) {
	store := newTestStore(t)
	before, err := store.CountActiveVehicles(context.Background())
	require.NoError(t, err)
	id := uniqueVehicleID(t)
	cleanupVehicleID(t, store, id)
	_, err = store.UpsertVehicle(context.Background(), id, "Bus", "")
	require.NoError(t, err)
	after, err := store.CountActiveVehicles(context.Background())
	require.NoError(t, err)
	assert.Equal(t, before+1, after)

	_, err = store.CountActiveTrips(context.Background())
	require.NoError(t, err) // exact value covered by trip tests; here just exercises the query
}

// TestStore_ListVehiclesPage covers the paged listing: limit and offset are
// honoured, consecutive pages tile the table exactly (the id tiebreaker in
// the ORDER BY keeps LIMIT/OFFSET from skipping or repeating a row), and an
// offset past the end is an empty page rather than an error.
func TestStore_ListVehiclesPage(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	ids := []string{"page-a", "page-b", "page-c", "page-d", "page-e"}
	for _, id := range ids {
		_, err := store.UpsertVehicle(ctx, id, "Label "+id, "agency")
		require.NoError(t, err)
	}

	first, err := store.ListVehiclesPage(ctx, VehicleFilter{IncludeInactive: true, Limit: 3, Offset: 0})
	require.NoError(t, err)
	require.Len(t, first, 3)

	second, err := store.ListVehiclesPage(ctx, VehicleFilter{IncludeInactive: true, Limit: 3, Offset: 3})
	require.NoError(t, err)
	require.Len(t, second, 2)

	seen := make(map[string]int, len(ids))
	for _, page := range [][]VehicleResponse{first, second} {
		for _, v := range page {
			seen[v.ID]++
		}
	}
	assert.Len(t, seen, len(ids), "the two pages together must cover every vehicle")
	for _, id := range ids {
		assert.Equal(t, 1, seen[id], "vehicle %s must appear on exactly one page", id)
	}

	past, err := store.ListVehiclesPage(ctx, VehicleFilter{IncludeInactive: true, Limit: 3, Offset: 100})
	require.NoError(t, err)
	assert.NotNil(t, past, "an offset past the end should be [], not nil")
	assert.Empty(t, past)
}

func TestStore_ListVehiclesPage_Empty(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)

	vehicles, err := store.ListVehiclesPage(context.Background(), VehicleFilter{IncludeInactive: true, Limit: 50, Offset: 0})
	require.NoError(t, err)
	assert.NotNil(t, vehicles, "empty page should be [], not nil")
	assert.Empty(t, vehicles)
}

// TestStore_ListVehiclesPage_ExcludesInactive verifies includeInactive=false
// filters deactivated vehicles out in SQL. The admin list hides them by
// default, and filtering after the fetch would shrink pages below the page
// size instead of returning a full page of active vehicles.
func TestStore_ListVehiclesPage_ExcludesInactive(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "page-active", "Active Bus", "agency")
	require.NoError(t, err)
	_, err = store.UpsertVehicle(ctx, "page-retired", "Retired Bus", "agency")
	require.NoError(t, err)
	require.NoError(t, store.DeactivateVehicle(ctx, "page-retired"))

	activeOnly, err := store.ListVehiclesPage(ctx, VehicleFilter{Limit: 50, Offset: 0})
	require.NoError(t, err)
	require.Len(t, activeOnly, 1)
	assert.Equal(t, "page-active", activeOnly[0].ID)

	all, err := store.ListVehiclesPage(ctx, VehicleFilter{IncludeInactive: true, Limit: 50, Offset: 0})
	require.NoError(t, err)
	assert.Len(t, all, 2, "includeInactive must return deactivated vehicles too")
}

// TestStore_ListVehicles_SafetyBound verifies the unpaged listing stops at
// the query's 1000-row LIMIT rather than marshalling an unbounded table.
// The rows are seeded with one generate_series insert to keep this fast.
func TestStore_ListVehicles_SafetyBound(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.pool.Exec(ctx,
		`INSERT INTO vehicles (id, label) SELECT 'bound-' || g, 'Bound ' || g FROM generate_series(1, 1001) AS g`)
	require.NoError(t, err)

	vehicles, err := store.ListVehicles(ctx)
	require.NoError(t, err)
	assert.Len(t, vehicles, 1000, "ListVehicles must stop at its 1000-row safety bound")
}

// TestStore_ListVehiclesPage_EmptyFilterMatchesAll pins the behaviour every
// existing caller relied on before filters existed: a filter carrying only
// paging returns the active fleet, newest-first, exactly as the unfiltered
// page always did.
func TestStore_ListVehiclesPage_EmptyFilterMatchesAll(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	for _, id := range []string{"empty-a", "empty-b", "empty-c"} {
		_, err := store.UpsertVehicle(ctx, id, "Label "+id, "agency")
		require.NoError(t, err)
	}
	require.NoError(t, store.DeactivateVehicle(ctx, "empty-c"))

	vehicles, err := store.ListVehiclesPage(ctx, VehicleFilter{Limit: 50, Offset: 0})
	require.NoError(t, err)
	require.Len(t, vehicles, 2, "a zero-value filter must still hide deactivated vehicles")

	ids := []string{vehicles[0].ID, vehicles[1].ID}
	assert.ElementsMatch(t, []string{"empty-a", "empty-b"}, ids)
}

// TestStore_ListVehiclesPage_FilterByQ covers the free-text search: it must
// match on id as well as label, and must be case-insensitive (ILIKE), since
// an operator hunting for a bus types whatever is painted on its side.
func TestStore_ListVehiclesPage_FilterByQ(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "kbz-1042", "Nairobi Express", "agency")
	require.NoError(t, err)
	_, err = store.UpsertVehicle(ctx, "kbz-2000", "Mombasa Local", "agency")
	require.NoError(t, err)

	byID, err := store.ListVehiclesPage(ctx, VehicleFilter{Q: "1042", Limit: 50})
	require.NoError(t, err)
	require.Len(t, byID, 1, "q must match on id")
	assert.Equal(t, "kbz-1042", byID[0].ID)

	byLabel, err := store.ListVehiclesPage(ctx, VehicleFilter{Q: "mombasa", Limit: 50})
	require.NoError(t, err)
	require.Len(t, byLabel, 1, "q must match on label, case-insensitively")
	assert.Equal(t, "kbz-2000", byLabel[0].ID)

	none, err := store.ListVehiclesPage(ctx, VehicleFilter{Q: "kisumu", Limit: 50})
	require.NoError(t, err)
	assert.NotNil(t, none, "no matches should be [], not nil")
	assert.Empty(t, none)
}

// TestStore_ListVehiclesPage_QEscapesWildcards is the guard for the shared
// LIKE escaping. Vehicle ids permit dots, hyphens and underscores, so an
// underscore in a search term is a realistic input, not a theoretical one:
// unescaped it is a single-character wildcard, and "bus_1" would also match
// "bus-1". A bare "%" must likewise match nothing rather than everything.
func TestStore_ListVehiclesPage_QEscapesWildcards(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	for _, id := range []string{"bus_1", "bus-1"} {
		_, err := store.UpsertVehicle(ctx, id, "Label "+id, "agency")
		require.NoError(t, err)
	}

	underscore, err := store.ListVehiclesPage(ctx, VehicleFilter{Q: "bus_1", Limit: 50})
	require.NoError(t, err)
	require.Len(t, underscore, 1, "the underscore must be a literal, not a wildcard")
	assert.Equal(t, "bus_1", underscore[0].ID)

	percent, err := store.ListVehiclesPage(ctx, VehicleFilter{Q: "%", Limit: 50})
	require.NoError(t, err)
	assert.Empty(t, percent, "a literal %% matches no id or label here, so it must not return everything")
}

// TestStore_ListVehiclesPage_FilterByAgency covers the exact-match agency
// filter: an agency tag selects only its own fleet, and the filter is not a
// substring match.
func TestStore_ListVehiclesPage_FilterByAgency(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	_, err := store.UpsertVehicle(ctx, "agency-a1", "A One", "nairobi")
	require.NoError(t, err)
	_, err = store.UpsertVehicle(ctx, "agency-b1", "B One", "mombasa")
	require.NoError(t, err)

	nairobi, err := store.ListVehiclesPage(ctx, VehicleFilter{AgencyTag: "nairobi", Limit: 50})
	require.NoError(t, err)
	require.Len(t, nairobi, 1)
	assert.Equal(t, "agency-a1", nairobi[0].ID)

	partial, err := store.ListVehiclesPage(ctx, VehicleFilter{AgencyTag: "nair", Limit: 50})
	require.NoError(t, err)
	assert.Empty(t, partial, "agency_tag is an exact match, not a substring one")
}

// TestStore_ListVehiclesPage_FiltersCombine checks the filters AND together
// rather than each merely working alone: only the vehicle satisfying all
// three conditions comes back.
func TestStore_ListVehiclesPage_FiltersCombine(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	seed := []struct {
		id, label, agency string
		deactivate        bool
	}{
		{"combo-match", "Express One", "nairobi", false},
		{"combo-other-agency", "Express Two", "mombasa", false},
		{"combo-inactive", "Express Three", "nairobi", true},
		{"combo-no-match", "Local Four", "nairobi", false},
	}
	for _, s := range seed {
		_, err := store.UpsertVehicle(ctx, s.id, s.label, s.agency)
		require.NoError(t, err)
		if s.deactivate {
			require.NoError(t, store.DeactivateVehicle(ctx, s.id))
		}
	}

	got, err := store.ListVehiclesPage(ctx, VehicleFilter{AgencyTag: "nairobi", Q: "express", Limit: 50})
	require.NoError(t, err)
	require.Len(t, got, 1, "q, agency_tag and the active filter must AND together")
	assert.Equal(t, "combo-match", got[0].ID)
}

// TestStore_ListVehiclesPage_FilterWithPaging checks the filter survives the
// LIMIT/OFFSET window: the two pages of a filtered result tile the matching
// set exactly, with nothing skipped, repeated, or leaking in from outside
// the filter.
func TestStore_ListVehiclesPage_FilterWithPaging(t *testing.T) {
	store := newTestStore(t)
	cleanupVehicles(t, store)
	ctx := context.Background()

	matching := []string{"pf-match-1", "pf-match-2", "pf-match-3"}
	for _, id := range matching {
		_, err := store.UpsertVehicle(ctx, id, "Match "+id, "agency")
		require.NoError(t, err)
	}
	for _, id := range []string{"pf-other-1", "pf-other-2"} {
		_, err := store.UpsertVehicle(ctx, id, "Other "+id, "agency")
		require.NoError(t, err)
	}

	filter := VehicleFilter{Q: "pf-match", Limit: 2}
	first, err := store.ListVehiclesPage(ctx, filter)
	require.NoError(t, err)
	require.Len(t, first, 2)

	filter.Offset = 2
	second, err := store.ListVehiclesPage(ctx, filter)
	require.NoError(t, err)
	require.Len(t, second, 1, "page 2 must still be filtered, not the unfiltered remainder")

	seen := make(map[string]int, len(matching))
	for _, page := range [][]VehicleResponse{first, second} {
		for _, v := range page {
			seen[v.ID]++
		}
	}
	assert.Len(t, seen, len(matching), "the two pages together must cover every match")
	for _, id := range matching {
		assert.Equal(t, 1, seen[id], "vehicle %s must appear on exactly one page", id)
	}
}
