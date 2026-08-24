package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBootstrapStore is an in-memory adminBootstrapStore double, used so the
// zero-admins/nonzero-admins branches of bootstrapAdmin can be tested without
// depending on the shared dev DB's current admin count (which the real store
// can't guarantee is zero — see TestBootstrapAdminRealStore below).
type fakeBootstrapStore struct {
	adminCount int
	created    []string // emails passed to CreateUser
	countErr   error
	createErr  error
}

func (f *fakeBootstrapStore) CountUsersByRole(_ context.Context, role string) (int, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	if role == "admin" {
		return f.adminCount, nil
	}
	return 0, nil
}

func (f *fakeBootstrapStore) CreateUser(_ context.Context, name, email, _, role string) (*UserResponse, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, email)
	return &UserResponse{Name: name, Email: email, Role: role}, nil
}

func TestBootstrapAdminCreatesWhenZeroAdmins(t *testing.T) {
	store := &fakeBootstrapStore{adminCount: 0}
	err := bootstrapAdmin(context.Background(), store, "admin@example.com", "supersecret123")
	require.NoError(t, err)
	assert.Equal(t, []string{"admin@example.com"}, store.created)
}

func TestBootstrapAdminSkipsWhenAdminsExist(t *testing.T) {
	store := &fakeBootstrapStore{adminCount: 1}
	err := bootstrapAdmin(context.Background(), store, "admin@example.com", "supersecret123")
	require.NoError(t, err)
	assert.Empty(t, store.created, "must not create a duplicate admin when one already exists")
}

func TestBootstrapAdminRejectsShortPassword(t *testing.T) {
	store := &fakeBootstrapStore{adminCount: 0}
	err := bootstrapAdmin(context.Background(), store, "admin@example.com", "short")
	require.Error(t, err)
	assert.Empty(t, store.created, "must not create an admin when the password is rejected")
}

func TestBootstrapAdminPropagatesCountError(t *testing.T) {
	store := &fakeBootstrapStore{countErr: assert.AnError}
	err := bootstrapAdmin(context.Background(), store, "admin@example.com", "supersecret123")
	require.Error(t, err)
}

func TestBootstrapAdminPropagatesCreateError(t *testing.T) {
	store := &fakeBootstrapStore{adminCount: 0, createErr: assert.AnError}
	err := bootstrapAdmin(context.Background(), store, "admin@example.com", "supersecret123")
	require.Error(t, err)
}

// TestBootstrapAdminRealStore is a smoke test against the real Postgres-backed
// store (skipped unless DATABASE_URL is set). The shared dev DB may already
// hold admins, so unlike the fake-based tests above this only asserts
// bootstrapAdmin runs the real CountUsersByRole/CreateUser SQL without error
// — it does not assert whether a new admin was actually created.
func TestBootstrapAdminRealStore(t *testing.T) {
	store := newTestStore(t)
	email := uniqueEmail(t)
	err := bootstrapAdmin(context.Background(), store, email, "supersecret123")
	require.NoError(t, err)
}
