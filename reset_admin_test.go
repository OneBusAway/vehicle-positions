package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResetAdminStore holds at most one user, keyed by email.
type fakeResetAdminStore struct {
	user     *User
	created  *UserResponse
	calls    []string
	password string
}

func (f *fakeResetAdminStore) GetUserByEmail(_ context.Context, email string) (*User, error) {
	if f.user == nil || f.user.Email != email {
		return nil, ErrUserNotFound
	}
	return f.user, nil
}

func (f *fakeResetAdminStore) CreateUser(_ context.Context, name, email, password, role string) (*UserResponse, error) {
	f.calls = append(f.calls, "create")
	f.password = password
	f.created = &UserResponse{ID: 1, Name: name, Email: email, Role: role, Active: true}
	return f.created, nil
}

func (f *fakeResetAdminStore) UpdateUser(_ context.Context, _ int64, _, _, role string) (*UserResponse, error) {
	f.calls = append(f.calls, "role:"+role)
	f.user.Role = role
	return &UserResponse{}, nil
}

func (f *fakeResetAdminStore) SetUserPasswordAndRevokeSessions(_ context.Context, _ int64, password string) error {
	f.calls = append(f.calls, "password")
	f.password = password
	return nil
}

func (f *fakeResetAdminStore) SetUserActiveAndRevokeSessions(_ context.Context, _ int64, active bool) error {
	f.calls = append(f.calls, "active")
	f.user.Active = active
	return nil
}

func TestResetAdmin_CreatesMissingAccount(t *testing.T) {
	store := &fakeResetAdminStore{}
	require.NoError(t, resetAdmin(context.Background(), store, "ops@example.org", "new-password"))
	assert.Equal(t, []string{"create"}, store.calls)
	assert.Equal(t, roleAdmin, store.created.Role)
	assert.Equal(t, "new-password", store.password)
}

// TestResetAdmin_RestoresLockedOutAdmin is the case it exists for: the only
// admin was deactivated, demoted, or forgot the password.
func TestResetAdmin_RestoresLockedOutAdmin(t *testing.T) {
	store := &fakeResetAdminStore{user: &User{ID: 3, Name: "Ops", Email: "ops@example.org", Role: roleDriver, Active: false}}
	require.NoError(t, resetAdmin(context.Background(), store, "ops@example.org", "new-password"))
	assert.Equal(t, []string{"role:admin", "active", "password"}, store.calls)
	assert.True(t, store.user.Active)
	assert.Equal(t, "new-password", store.password)

	store = &fakeResetAdminStore{user: &User{ID: 3, Email: "ops@example.org", Role: roleAdmin, Active: true}}
	require.NoError(t, resetAdmin(context.Background(), store, "ops@example.org", "new-password"))
	assert.Equal(t, []string{"password"}, store.calls, "an active admin only gets the new password")
}

func TestResetAdmin_RejectsWeakPassword(t *testing.T) {
	store := &fakeResetAdminStore{}
	assert.Error(t, resetAdmin(context.Background(), store, "ops@example.org", "short"))
	assert.Empty(t, store.calls)
}

func TestReadPassword(t *testing.T) {
	pw, err := readPassword(strings.NewReader("s3cret-pass\nignored\n"))
	require.NoError(t, err)
	assert.Equal(t, "s3cret-pass", pw)

	pw, err = readPassword(strings.NewReader("no-newline"))
	require.NoError(t, err)
	assert.Equal(t, "no-newline", pw)

	_, err = readPassword(strings.NewReader(""))
	assert.Error(t, err)
}

func TestRunResetAdmin_Usage(t *testing.T) {
	var stderr bytes.Buffer
	assert.Equal(t, 2, runResetAdmin(nil, strings.NewReader("pw\n"), &stderr))
	assert.Contains(t, stderr.String(), "usage: vehicle-positions reset-admin")
}
