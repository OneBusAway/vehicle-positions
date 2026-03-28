package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockAPIKeyStore struct {
	apiKey         *APIKey
	getErr         error
	lastUsedCalled bool
	lastUsedID     int64
	updateErr      error
}

// GetAPIKeyByHash returns the API key if getErr is nil, otherwise returns getErr.
func (m *mockAPIKeyStore) GetAPIKeyByHash(ctx context.Context, keyHash string) (*APIKey, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.apiKey, nil
}

// UpdateAPIKeyLastUsed updates the last used timestamp for the API key.
func (m *mockAPIKeyStore) UpdateAPIKeyLastUsed(ctx context.Context, id int64) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.lastUsedCalled = true
	m.lastUsedID = id
	return nil
}

// TestHashAPIKey verifies that the hashAPIKey function produces consistent and correctly sized hashes.
func TestHashAPIKey(t *testing.T) {
	h1 := hashAPIKey("abc123")
	h2 := hashAPIKey("abc123")
	h3 := hashAPIKey("different")

	assert.Equal(t, h1, h2)
	assert.NotEqual(t, h1, h3)
	assert.Len(t, h1, 64)
}

// TestRequireAPIKey tests the requireAPIKey middleware with various scenarios, including missing header, invalid key, inactive key, store failure, update failure, and valid key.
func TestRequireAPIKey_MissingHeader(t *testing.T) {
	store := &mockAPIKeyStore{}
	handler := requireAPIKey(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/gtfs-rt/vehicle-positions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// Invalid API key should result in 401 Unauthorized
func TestRequireAPIKey_InvalidKey(t *testing.T) {
	store := &mockAPIKeyStore{getErr: pgx.ErrNoRows}
	handler := requireAPIKey(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/gtfs-rt/vehicle-positions", nil)
	req.Header.Set("X-API-Key", "bad-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// Inactive API key should result in 401 Unauthorized
func TestRequireAPIKey_InactiveKey(t *testing.T) {
	store := &mockAPIKeyStore{
		apiKey: &APIKey{
			ID:     1,
			Name:   "test",
			Active: false,
		},
	}
	handler := requireAPIKey(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/gtfs-rt/vehicle-positions", nil)
	req.Header.Set("X-API-Key", "inactive-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.False(t, store.lastUsedCalled)
}

// Store failure should result in 500 Internal Server Error
func TestRequireAPIKey_StoreFailure(t *testing.T) {
	store := &mockAPIKeyStore{getErr: errors.New("db down")}
	handler := requireAPIKey(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/gtfs-rt/vehicle-positions", nil)
	req.Header.Set("X-API-Key", "some-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// Update last_used_at failure should be logged but must not block feed access.
func TestRequireAPIKey_UpdateLastUsedFailure_DoesNotBlockRequest(t *testing.T) {
	store := &mockAPIKeyStore{
		apiKey: &APIKey{
			ID:     7,
			Name:   "feed consumer",
			Active: true,
		},
		updateErr: errors.New("update failed"),
	}

	called := false
	handler := requireAPIKey(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/gtfs-rt/vehicle-positions", nil)
	req.Header.Set("X-API-Key", "valid-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
}

// Valid API key should call next handler and update last used timestamp
func TestRequireAPIKey_ValidKey(t *testing.T) {
	store := &mockAPIKeyStore{
		apiKey: &APIKey{
			ID:        42,
			Name:      "consumer",
			KeyHash:   hashAPIKey("valid-key"),
			Active:    true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}

	called := false
	handler := requireAPIKey(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/gtfs-rt/vehicle-positions", nil)
	req.Header.Set("X-API-Key", "valid-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, store.lastUsedCalled)
	assert.Equal(t, int64(42), store.lastUsedID)
}
