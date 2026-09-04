package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAPIKeyManager is the admin CRUD surface, recording what the handlers
// persist so tests can assert on the stored row rather than the response only.
type fakeAPIKeyManager struct {
	keys          []APIKey
	createErr     error
	listErr       error
	deactivateErr error
	deactivated   []int64
}

func (f *fakeAPIKeyManager) CreateAPIKey(_ context.Context, name, keyHash string) (*APIKey, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	key := APIKey{
		ID:        int64(len(f.keys) + 1),
		Name:      name,
		KeyHash:   keyHash,
		Active:    true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	f.keys = append(f.keys, key)
	return &key, nil
}

func (f *fakeAPIKeyManager) ListAPIKeys(_ context.Context) ([]APIKey, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	keys := make([]APIKey, 0, len(f.keys))
	return append(keys, f.keys...), nil
}

func (f *fakeAPIKeyManager) DeactivateAPIKey(_ context.Context, id int64) error {
	if f.deactivateErr != nil {
		return f.deactivateErr
	}
	f.deactivated = append(f.deactivated, id)
	return nil
}

// createAPIKeyRequestWith posts a raw body to handleCreateAPIKey under the
// given Content-Type.
func createAPIKeyRequestWith(store APIKeyManager, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/api-keys", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	handleCreateAPIKey(store)(w, req)
	return w
}

func TestHandleCreateAPIKey_Validation(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantError   string
	}{
		{
			name:        "name at the length limit",
			contentType: "application/json",
			body:        `{"name":"` + strings.Repeat("a", maxFieldLength) + `"}`,
			wantStatus:  http.StatusCreated,
		},
		{
			name:        "name past the length limit",
			contentType: "application/json",
			body:        `{"name":"` + strings.Repeat("a", maxFieldLength+1) + `"}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "name must be at most 255 characters",
		},
		{
			name:        "empty name",
			contentType: "application/json",
			body:        `{"name":""}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "name is required",
		},
		{
			name:        "missing content type",
			contentType: "",
			body:        `{"name":"feed"}`,
			wantStatus:  http.StatusUnsupportedMediaType,
			wantError:   "Content-Type must be application/json",
		},
		{
			name:        "non-JSON content type",
			contentType: "text/plain",
			body:        `{"name":"feed"}`,
			wantStatus:  http.StatusUnsupportedMediaType,
			wantError:   "Content-Type must be application/json",
		},
		{
			name:        "unknown field",
			contentType: "application/json",
			body:        `{"name":"feed","active":true}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "unknown field",
		},
		{
			name:        "trailing data",
			contentType: "application/json",
			body:        `{"name":"feed"}{"name":"second"}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "no trailing data",
		},
		{
			name:        "empty body",
			contentType: "application/json",
			body:        ``,
			wantStatus:  http.StatusBadRequest,
			wantError:   "invalid JSON",
		},
		{
			name:        "malformed JSON",
			contentType: "application/json",
			body:        `{"name":`,
			wantStatus:  http.StatusBadRequest,
			wantError:   "invalid JSON",
		},
		{
			name:        "wrong field type",
			contentType: "application/json",
			body:        `{"name":42}`,
			wantStatus:  http.StatusBadRequest,
			wantError:   `field "name" has invalid type`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := createAPIKeyRequestWith(&fakeAPIKeyManager{}, tc.contentType, tc.body)

			require.Equal(t, tc.wantStatus, w.Code)
			if tc.wantError != "" {
				assert.Contains(t, decodeError(t, w), tc.wantError)
			}
		})
	}
}

// TestHandleCreateAPIKey_ReturnsRawKeyOnce checks the one moment the raw key
// is visible: the 201 body carries it, and only its hash is persisted.
func TestHandleCreateAPIKey_ReturnsRawKeyOnce(t *testing.T) {
	store := &fakeAPIKeyManager{}

	w := createAPIKeyRequestWith(store, "application/json", `{"name":"transit-app"}`)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp createAPIKeyResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	assert.Len(t, resp.Key, 64, "the response carries a freshly generated raw key")
	assert.Equal(t, "transit-app", resp.Name)
	assert.True(t, resp.Active)
	assert.Nil(t, resp.LastUsedAt, "a new key has never been used")

	require.Len(t, store.keys, 1)
	assert.Equal(t, hashAPIKey(resp.Key), store.keys[0].KeyHash, "only the hash is stored")
	assert.NotContains(t, store.keys[0].KeyHash, resp.Key)
}

func TestHandleCreateAPIKey_StoreError(t *testing.T) {
	w := createAPIKeyRequestWith(&fakeAPIKeyManager{createErr: errors.New("insert failed")}, "application/json", `{"name":"feed"}`)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", decodeError(t, w))
}

func TestHandleCreateAPIKey_OversizedBodyRejected(t *testing.T) {
	w := createAPIKeyRequestWith(&fakeAPIKeyManager{}, "application/json", `{"name":"`+strings.Repeat("a", 2<<10)+`"}`)

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Equal(t, "request body too large", decodeError(t, w))
}

func listAPIKeys(store APIKeyManager) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/api-keys", nil)
	w := httptest.NewRecorder()
	handleListAPIKeys(store)(w, req)
	return w
}

// TestHandleListAPIKeys_NeverLeaksHash is the counterpart to the `json:"-"`
// tag on APIKey.KeyHash: the listing must expose metadata only.
func TestHandleListAPIKeys_NeverLeaksHash(t *testing.T) {
	const hash = "df1a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8"
	lastUsed := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	store := &fakeAPIKeyManager{keys: []APIKey{
		{ID: 1, Name: "transit-app", KeyHash: hash, Active: true, LastUsedAt: &lastUsed},
		{ID: 2, Name: "revoked", KeyHash: hash + "00", Active: false},
	}}

	w := listAPIKeys(store)
	require.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()
	assert.NotContains(t, body, "key_hash")
	assert.NotContains(t, body, hash)

	var keys []APIKey
	require.NoError(t, json.Unmarshal([]byte(body), &keys))
	require.Len(t, keys, 2)
	assert.Equal(t, "transit-app", keys[0].Name)
	assert.Equal(t, lastUsed, keys[0].LastUsedAt.UTC())
	assert.False(t, keys[1].Active)
	assert.Nil(t, keys[1].LastUsedAt, "a key that was never used serializes as null")
}

func TestHandleListAPIKeys_EmptyReturnsArray(t *testing.T) {
	w := listAPIKeys(&fakeAPIKeyManager{})

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "[]", strings.TrimSpace(w.Body.String()), "an empty list must not serialize as null")
}

func TestHandleListAPIKeys_StoreError(t *testing.T) {
	w := listAPIKeys(&fakeAPIKeyManager{listErr: errors.New("query failed")})

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", decodeError(t, w))
}

func deactivateAPIKey(store APIKeyManager, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/api-keys/"+id, nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	handleDeactivateAPIKey(store)(w, req)
	return w
}

func TestHandleDeactivateAPIKey_Success(t *testing.T) {
	store := &fakeAPIKeyManager{}

	w := deactivateAPIKey(store, "3")

	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
	assert.Equal(t, []int64{3}, store.deactivated)
}

func TestHandleDeactivateAPIKey_NotFound(t *testing.T) {
	w := deactivateAPIKey(&fakeAPIKeyManager{deactivateErr: ErrAPIKeyNotFound}, "404")

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "api key not found", decodeError(t, w))
}

func TestHandleDeactivateAPIKey_StoreError(t *testing.T) {
	w := deactivateAPIKey(&fakeAPIKeyManager{deactivateErr: errors.New("update failed")}, "1")

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", decodeError(t, w))
}

func TestHandleDeactivateAPIKey_InvalidID(t *testing.T) {
	for _, id := range []string{"abc", "0", "-1", ""} {
		t.Run("id="+id, func(t *testing.T) {
			store := &fakeAPIKeyManager{}

			w := deactivateAPIKey(store, id)

			require.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, "invalid api key id", decodeError(t, w))
			assert.Empty(t, store.deactivated, "a rejected id must never reach the store")
		})
	}
}
