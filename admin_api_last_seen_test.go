package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/db"
)

// seenAt is a sign-in time, in UTC as the store hands it over.
var seenAt = time.Date(2026, 11, 20, 10, 30, 0, 0, time.UTC)

// userFields decodes a user object's fields raw, so a test can tell a null
// field from one that is missing.
func userFields(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	return fields
}

// TestHandleListUsers_LastSeenAt: each user in the list carries last_seen_at,
// null for one who has never signed in.
func TestHandleListUsers_LastSeenAt(t *testing.T) {
	store := &mockUserPager{users: []UserResponse{
		{ID: 1, Name: "Never", Email: "never@example.com", Role: "driver", Active: true},
		{ID: 2, Name: "Seen", Email: "seen@example.com", Role: "driver", Active: true, LastSeenAt: &seenAt},
	}}
	rec := httptest.NewRecorder()
	handleListUsers(store).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var body struct {
		Users []json.RawMessage `json:"users"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Users, 2)
	assert.Equal(t, "null", string(userFields(t, body.Users[0])["last_seen_at"]))
	assert.Equal(t, `"2026-11-20T10:30:00Z"`, string(userFields(t, body.Users[1])["last_seen_at"]))
}

// TestHandleGetUser_LastSeenAt: one user carries last_seen_at too.
func TestHandleGetUser_LastSeenAt(t *testing.T) {
	for want, user := range map[string]*UserResponse{
		"null":                   {ID: 1, Name: "Never", Email: "never@example.com", Role: "driver", Active: true},
		`"2026-11-20T10:30:00Z"`: {ID: 2, Name: "Seen", Email: "seen@example.com", Role: "driver", Active: true, LastSeenAt: &seenAt},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/1", nil)
		req.SetPathValue("id", "1")
		rec := httptest.NewRecorder()
		handleGetUser(&mockUserGetter{user: user}).ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, want, string(userFields(t, rec.Body.Bytes())["last_seen_at"]), user.Name)
	}
}

// TestHandleFeedHealth_VehiclesEachMinute: the health answer carries how many
// vehicles had a live report accepted in each minute, as the Health page draws
// it.
func TestHandleFeedHealth_VehiclesEachMinute(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)
	sendReport(t, h, "/api/v1/locations", "bus-1", http.StatusCreated)
	sendReport(t, h, "/api/v1/locations", "bus-1", http.StatusCreated)
	clock.advance(time.Minute)
	sendReport(t, h, "/api/v1/locations", "bus-1", http.StatusCreated)
	sendReport(t, h, "/api/v1/locations", "bus-2", http.StatusCreated)

	rec := httptest.NewRecorder()
	handleFeedHealth(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/health", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Minutes []map[string]json.RawMessage `json:"minutes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Minutes, healthWindowMinutes)
	last := healthWindowMinutes - 1
	assert.Equal(t, "0", string(body.Minutes[0]["vehicles"]), "a minute with no reports counts no vehicles")
	assert.Equal(t, "1", string(body.Minutes[last-1]["vehicles"]), "a vehicle counts once in its minute")
	assert.Equal(t, "2", string(body.Minutes[last]["vehicles"]))
}

// TestStore_UsersCarryLastSeen: every read of a user, and the user an edit
// returns, carries last_seen_at in UTC, nil until the user signs in.
func TestStore_UsersCarryLastSeen(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const email = "api-last-seen@example.com"
	cleanupTestUsers(t, store, email)
	t.Cleanup(func() { cleanupTestUsers(t, store, email) })

	created, err := store.CreateUser(ctx, "API Last Seen", email, "securepass", "driver")
	require.NoError(t, err)
	assert.Nil(t, created.LastSeenAt)
	listed := func() UserResponse {
		t.Helper()
		users, err := store.ListUsers(ctx)
		require.NoError(t, err)
		for _, u := range users {
			if u.ID == created.ID {
				return u
			}
		}
		require.FailNow(t, "user not listed")
		return UserResponse{}
	}
	got, err := store.GetUser(ctx, created.ID)
	require.NoError(t, err)
	assert.Nil(t, got.LastSeenAt, "never signed in")
	assert.Nil(t, listed().LastSeenAt)

	require.NoError(t, store.UpdateUserLastSeen(ctx, created.ID))
	got, err = store.GetUser(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastSeenAt)
	assert.WithinDuration(t, time.Now(), *got.LastSeenAt, time.Minute)
	assert.Equal(t, time.UTC, got.LastSeenAt.Location())
	require.NotNil(t, listed().LastSeenAt)
	assert.True(t, listed().LastSeenAt.Equal(*got.LastSeenAt))
	page, err := store.ListUsersPage(ctx, UserFilter{Q: email, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.NotNil(t, page[0].LastSeenAt)
	assert.Equal(t, time.UTC, page[0].LastSeenAt.Location())

	edited, err := store.UpdateUser(ctx, created.ID, "Renamed", email, "driver")
	require.NoError(t, err)
	require.NotNil(t, edited.LastSeenAt, "an edit returns the user as it is, last seen included")
	assert.True(t, edited.LastSeenAt.Equal(*got.LastSeenAt))
}

// TestStore_LastSeenTwiceInOneTransaction: NOW() is the same all through a
// transaction, so a second stamp in it would write the value already there,
// and the trigger, which skips only a change to last_seen_at, would move
// updated_at. The second stamp changes nothing instead. An edit still moves
// updated_at.
func TestStore_LastSeenTwiceInOneTransaction(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const email = "last-seen-twice@example.com"
	cleanupTestUsers(t, store, email)
	t.Cleanup(func() { cleanupTestUsers(t, store, email) })

	created, err := store.CreateUser(ctx, "Twice", email, "securepass", "driver")
	require.NoError(t, err)
	updatedAt := func() time.Time {
		t.Helper()
		u, err := store.GetUser(ctx, created.ID)
		require.NoError(t, err)
		return u.UpdatedAt
	}
	before := updatedAt()

	tx, err := store.pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)
	require.NoError(t, q.UpdateUserLastSeen(ctx, created.ID))
	require.NoError(t, q.UpdateUserLastSeen(ctx, created.ID))
	require.NoError(t, tx.Commit(ctx))
	assert.True(t, updatedAt().Equal(before), "signing in twice in one transaction is still not an edit")

	_, err = store.UpdateUser(ctx, created.ID, "Edited", email, "driver")
	require.NoError(t, err)
	assert.True(t, updatedAt().After(before), "an edit still moves updated_at")
}
