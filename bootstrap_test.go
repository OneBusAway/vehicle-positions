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

// TestBootstrapAdminRejectsInvalidEmail: the admin sign-in form is
// type="email", so a browser will not submit "admin" and strips the spaces
// and line breaks around an address. An account created with any of these
// could not sign in to the admin UI, and bootstrap would never run again.
func TestBootstrapAdminRejectsInvalidEmail(t *testing.T) {
	cases := []struct{ name, email string }{
		{"no domain", "admin"},
		{"leading space", " ops@example.org"},
		{"trailing newline", "ops@example.org\n"},
		{"display name", "Ops <ops@example.org>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeBootstrapStore{adminCount: 0}
			err := bootstrapAdmin(context.Background(), store, tc.email, "supersecret123")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "is not valid")
			assert.Empty(t, store.created, "must not create an admin when the email is rejected")
		})
	}
}

// Once an admin exists the bootstrap variables do nothing, so a bad email left
// in the environment must not stop a later boot.
func TestBootstrapAdminIgnoresInvalidEmailOnceAdminsExist(t *testing.T) {
	store := &fakeBootstrapStore{adminCount: 1}
	require.NoError(t, bootstrapAdmin(context.Background(), store, "admin", "supersecret123"))
	assert.Empty(t, store.created)
}

func TestBootstrapAdminPropagatesCountError(t *testing.T) {
	store := &fakeBootstrapStore{countErr: assert.AnError}
	err := bootstrapAdmin(context.Background(), store, "admin@example.com", "supersecret123")
	require.Error(t, err)
}

// Concurrent startup: both instances read zero admins, one INSERT wins on the
// email unique index, the other must not fail startup.
func TestBootstrapAdminTreatsDuplicateEmailAsAlreadyBootstrapped(t *testing.T) {
	store := &fakeBootstrapStore{adminCount: 0, createErr: ErrDuplicateEmail}
	err := bootstrapAdmin(context.Background(), store, "admin@example.com", "supersecret123")
	require.NoError(t, err)
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
