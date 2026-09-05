package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockVehicleStore struct {
	vehicles map[string]*VehicleResponse
	err      error

	// Recorded by ListVehiclesPage so paging tests can assert what the
	// handler asked the store for.
	gotIncludeInactive bool
	gotLimit           int32
	gotOffset          int32
}

// pageSlice returns the limit/offset window of s, mirroring SQL LIMIT/OFFSET
// semantics (an offset past the end is an empty page, never nil). Shared by
// the in-memory paging doubles in this package.
func pageSlice[T any](s []T, limit, offset int32) []T {
	if int(offset) >= len(s) {
		return make([]T, 0)
	}
	end := int(offset) + int(limit)
	if end > len(s) {
		end = len(s)
	}
	return append(make([]T, 0, end-int(offset)), s[int(offset):end]...)
}

func newMockVehicleStore() *mockVehicleStore {
	return &mockVehicleStore{vehicles: make(map[string]*VehicleResponse)}
}

func (m *mockVehicleStore) ListVehicles(_ context.Context) ([]VehicleResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	result := make([]VehicleResponse, 0, len(m.vehicles))
	for _, v := range m.vehicles {
		result = append(result, *v)
	}
	return result, nil
}

// ListVehiclesPage orders by id so pages are deterministic; the real store
// orders by created_at DESC with an id tiebreaker. Only the totality of the
// order matters to the handler.
func (m *mockVehicleStore) ListVehiclesPage(_ context.Context, includeInactive bool, limit, offset int32) ([]VehicleResponse, error) {
	m.gotIncludeInactive, m.gotLimit, m.gotOffset = includeInactive, limit, offset
	if m.err != nil {
		return nil, m.err
	}
	ids := make([]string, 0, len(m.vehicles))
	for id := range m.vehicles {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	all := make([]VehicleResponse, 0, len(ids))
	for _, id := range ids {
		v := m.vehicles[id]
		if !includeInactive && !v.Active {
			continue
		}
		all = append(all, *v)
	}
	return pageSlice(all, limit, offset), nil
}

func (m *mockVehicleStore) GetVehicle(_ context.Context, id string) (*VehicleResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	v, ok := m.vehicles[id]
	if !ok {
		return nil, fmt.Errorf("get vehicle: %w", pgx.ErrNoRows)
	}
	return v, nil
}

func (m *mockVehicleStore) UpsertVehicle(_ context.Context, id, label, agencyTag string) (*VehicleResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	now := time.Now()
	if v, exists := m.vehicles[id]; exists {
		v.Label = label
		v.AgencyTag = agencyTag
		v.Active = true
		v.UpdatedAt = now
		return v, nil
	}
	v := &VehicleResponse{
		ID:        id,
		Label:     label,
		AgencyTag: agencyTag,
		Active:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.vehicles[id] = v
	return v, nil
}

func (m *mockVehicleStore) DeactivateVehicle(_ context.Context, id string) error {
	if m.err != nil {
		return m.err
	}
	v, ok := m.vehicles[id]
	if !ok {
		return fmt.Errorf("deactivate vehicle: %w", pgx.ErrNoRows)
	}
	v.Active = false
	return nil
}

func postVehicle(handler http.HandlerFunc, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/v1/admin/vehicles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandleListVehicles_WithVehicles(t *testing.T) {
	store := newMockVehicleStore()
	now := time.Now()
	store.vehicles["bus-1"] = &VehicleResponse{ID: "bus-1", Label: "Bus 1", Active: true, CreatedAt: now, UpdatedAt: now}
	store.vehicles["bus-2"] = &VehicleResponse{ID: "bus-2", Label: "Bus 2", Active: true, CreatedAt: now, UpdatedAt: now}

	handler := handleListVehicles(store)
	req := httptest.NewRequest("GET", "/api/v1/admin/vehicles", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var vehicles []VehicleResponse
	err := json.NewDecoder(w.Body).Decode(&vehicles)
	require.NoError(t, err)
	assert.Len(t, vehicles, 2)
}

func TestHandleListVehicles_ResponseNotNull(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleListVehicles(store)

	req := httptest.NewRequest("GET", "/api/v1/admin/vehicles", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify raw JSON is [] not null
	var raw json.RawMessage
	err := json.NewDecoder(w.Body).Decode(&raw)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(raw), "empty vehicle list must be JSON [] not null")
}

// TestHandleListVehicles_PagingParams covers the limit/offset contract: the
// defaults applied when the params are absent, the values that reach the
// store, and every rejection — including the pair at exactly the maximum and
// one past it. Rejections assert the error message, not just the status, so
// a case can't pass for the wrong reason.
func TestHandleListVehicles_PagingParams(t *testing.T) {
	const limitError = "limit must be between 1 and 200"
	offsetError := fmt.Sprintf("offset must be between 0 and %d", maxListOffset)

	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantError  string
		wantLimit  int32
		wantOffset int32
	}{
		{name: "absent params use the defaults", query: "", wantStatus: http.StatusOK, wantLimit: defaultListLimit},
		{name: "explicit limit and offset", query: "?limit=10&offset=20", wantStatus: http.StatusOK, wantLimit: 10, wantOffset: 20},
		{name: "limit at the maximum", query: "?limit=200", wantStatus: http.StatusOK, wantLimit: maxListLimit},
		{name: "limit one past the maximum", query: "?limit=201", wantStatus: http.StatusBadRequest, wantError: limitError},
		{name: "limit zero", query: "?limit=0", wantStatus: http.StatusBadRequest, wantError: limitError},
		{name: "limit negative", query: "?limit=-1", wantStatus: http.StatusBadRequest, wantError: limitError},
		{name: "limit non-numeric", query: "?limit=abc", wantStatus: http.StatusBadRequest, wantError: limitError},
		{name: "offset zero", query: "?offset=0", wantStatus: http.StatusOK, wantLimit: defaultListLimit},
		{name: "offset negative", query: "?offset=-1", wantStatus: http.StatusBadRequest, wantError: offsetError},
		{name: "offset non-numeric", query: "?offset=abc", wantStatus: http.StatusBadRequest, wantError: offsetError},
		{name: "offset at the maximum", query: "?offset=2147483647", wantStatus: http.StatusOK, wantLimit: defaultListLimit, wantOffset: maxListOffset},
		{name: "offset one past the maximum", query: "?offset=2147483648", wantStatus: http.StatusBadRequest, wantError: offsetError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMockVehicleStore()
			handler := handleListVehicles(store)
			req := httptest.NewRequest("GET", "/api/v1/admin/vehicles"+tt.query, nil)
			w := httptest.NewRecorder()
			handler(w, req)

			require.Equal(t, tt.wantStatus, w.Code)
			if tt.wantStatus != http.StatusOK {
				assert.Equal(t, tt.wantError, decodeErrorResponse(t, w))
				return
			}
			assert.Equal(t, tt.wantLimit, store.gotLimit, "limit passed to the store")
			assert.Equal(t, tt.wantOffset, store.gotOffset, "offset passed to the store")
		})
	}
}

// TestHandleListVehicles_PagesResults verifies limit/offset actually narrow
// the response rather than only being validated.
func TestHandleListVehicles_PagesResults(t *testing.T) {
	store := newMockVehicleStore()
	now := time.Now()
	for _, id := range []string{"bus-1", "bus-2", "bus-3"} {
		store.vehicles[id] = &VehicleResponse{ID: id, Label: id, Active: true, CreatedAt: now, UpdatedAt: now}
	}

	handler := handleListVehicles(store)
	req := httptest.NewRequest("GET", "/api/v1/admin/vehicles?limit=2&offset=1", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var vehicles []VehicleResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&vehicles))
	require.Len(t, vehicles, 2)
	assert.Equal(t, "bus-2", vehicles[0].ID)
	assert.Equal(t, "bus-3", vehicles[1].ID)
}

// TestHandleListVehicles_IncludesInactive pins that the endpoint still lists
// deactivated vehicles, which it has always done — the admin page's
// active-only filter must not leak into the API.
func TestHandleListVehicles_IncludesInactive(t *testing.T) {
	store := newMockVehicleStore()
	now := time.Now()
	store.vehicles["bus-1"] = &VehicleResponse{ID: "bus-1", Label: "Bus 1", Active: true, CreatedAt: now, UpdatedAt: now}
	store.vehicles["bus-2"] = &VehicleResponse{ID: "bus-2", Label: "Bus 2", Active: false, CreatedAt: now, UpdatedAt: now}

	handler := handleListVehicles(store)
	req := httptest.NewRequest("GET", "/api/v1/admin/vehicles", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, store.gotIncludeInactive, "the API must ask for deactivated vehicles too")
	var vehicles []VehicleResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&vehicles))
	assert.Len(t, vehicles, 2)
}

// TestHandleListVehicles_StillReturnsBareArray guards the response shape:
// adding paging deliberately did NOT wrap the body in an object the way the
// trips and location-history endpoints do, because existing clients and the
// documented schema index into a bare array. If someone wraps it later, this
// fails loudly.
func TestHandleListVehicles_StillReturnsBareArray(t *testing.T) {
	store := newMockVehicleStore()
	now := time.Now()
	store.vehicles["bus-1"] = &VehicleResponse{ID: "bus-1", Label: "Bus 1", Active: true, CreatedAt: now, UpdatedAt: now}

	handler := handleListVehicles(store)
	req := httptest.NewRequest("GET", "/api/v1/admin/vehicles", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var vehicles []VehicleResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &vehicles), "response must unmarshal into a bare array")
	require.Len(t, vehicles, 1)
	assert.Equal(t, "bus-1", vehicles[0].ID)
}

func TestHandleListVehicles_StoreError(t *testing.T) {
	store := newMockVehicleStore()
	store.err = fmt.Errorf("database down")

	handler := handleListVehicles(store)
	req := httptest.NewRequest("GET", "/api/v1/admin/vehicles", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "failed to list vehicles")
}

func TestHandleGetVehicle_Found(t *testing.T) {
	store := newMockVehicleStore()
	now := time.Now()
	store.vehicles["bus-1"] = &VehicleResponse{ID: "bus-1", Label: "Bus 1", AgencyTag: "nairobi", Active: true, CreatedAt: now, UpdatedAt: now}

	handler := handleGetVehicle(store)
	req := httptest.NewRequest("GET", "/api/v1/admin/vehicles/bus-1", nil)
	req.SetPathValue("id", "bus-1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var vehicle VehicleResponse
	err := json.NewDecoder(w.Body).Decode(&vehicle)
	require.NoError(t, err)
	assert.Equal(t, "bus-1", vehicle.ID)
	assert.Equal(t, "Bus 1", vehicle.Label)
	assert.Equal(t, "nairobi", vehicle.AgencyTag)
	assert.True(t, vehicle.Active)
}

func TestHandleGetVehicle_NotFound(t *testing.T) {
	store := newMockVehicleStore()

	handler := handleGetVehicle(store)
	req := httptest.NewRequest("GET", "/api/v1/admin/vehicles/unknown", nil)
	req.SetPathValue("id", "unknown")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "vehicle not found")
}

func TestHandleGetVehicle_StoreError(t *testing.T) {
	store := newMockVehicleStore()
	store.err = errors.New("database down")

	handler := handleGetVehicle(store)
	req := httptest.NewRequest("GET", "/api/v1/admin/vehicles/bus-1", nil)
	req.SetPathValue("id", "bus-1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "failed to get vehicle")
}

func TestHandleGetVehicle_InvalidID(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleGetVehicle(store)

	tests := []struct {
		name string
		id   string
		want string
	}{
		{
			name: "id with special characters",
			id:   "bus@1!",
			want: "alphanumeric characters",
		},
		{
			name: "id too long",
			id:   strings.Repeat("a", 51),
			want: "at most 50 characters",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/admin/vehicles/test", nil)
			req.SetPathValue("id", tc.id)
			w := httptest.NewRecorder()
			handler(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp map[string]string
			err := json.NewDecoder(w.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Contains(t, resp["error"], tc.want)
		})
	}
}

func TestHandleUpsertVehicle_CreateNew(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleUpsertVehicle(store)

	body := []byte(`{"id":"bus-1","label":"Bus 1","agency_tag":"nairobi"}`)
	w := postVehicle(handler, body)

	assert.Equal(t, http.StatusOK, w.Code)

	var vehicle VehicleResponse
	err := json.NewDecoder(w.Body).Decode(&vehicle)
	require.NoError(t, err)
	assert.Equal(t, "bus-1", vehicle.ID)
	assert.Equal(t, "Bus 1", vehicle.Label)
	assert.Equal(t, "nairobi", vehicle.AgencyTag)
	assert.True(t, vehicle.Active)
}

func TestHandleUpsertVehicle_UpdateExisting(t *testing.T) {
	store := newMockVehicleStore()
	now := time.Now()
	store.vehicles["bus-1"] = &VehicleResponse{ID: "bus-1", Label: "Old Label", AgencyTag: "old-tag", Active: true, CreatedAt: now, UpdatedAt: now}

	handler := handleUpsertVehicle(store)
	body := []byte(`{"id":"bus-1","label":"New Label","agency_tag":"new-tag"}`)
	w := postVehicle(handler, body)

	assert.Equal(t, http.StatusOK, w.Code)

	var vehicle VehicleResponse
	err := json.NewDecoder(w.Body).Decode(&vehicle)
	require.NoError(t, err)
	assert.Equal(t, "bus-1", vehicle.ID)
	assert.Equal(t, "New Label", vehicle.Label)
	assert.Equal(t, "new-tag", vehicle.AgencyTag)
}

func TestHandleUpsertVehicle_Validation(t *testing.T) {
	store := newMockVehicleStore()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing id",
			body: `{"label":"Bus 1"}`,
			want: "id is required",
		},
		{
			name: "id too long",
			body: `{"id":"abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz"}`,
			want: "id must be at most 50 characters",
		},
		{
			name: "id with spaces",
			body: `{"id":"bus 1"}`,
			want: "id must contain only alphanumeric characters",
		},
		{
			name: "id with special characters",
			body: `{"id":"bus@1!"}`,
			want: "id must contain only alphanumeric characters",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := handleUpsertVehicle(store)
			w := postVehicle(handler, []byte(tc.body))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			var resp map[string]string
			err := json.NewDecoder(w.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Contains(t, resp["error"], tc.want)
		})
	}
}

func TestHandleUpsertVehicle_ValidIDBoundary(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleUpsertVehicle(store)

	// Exactly 50 chars — accepted
	id := "abcdefghij-klmnopqrst_uvwxyz-1234567890_abcdefghij"
	require.Len(t, id, 50)

	body, err := json.Marshal(upsertVehicleRequest{ID: id, Label: "Test"})
	require.NoError(t, err)
	w := postVehicle(handler, body)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleUpsertVehicle_IDBoundary51Rejected(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleUpsertVehicle(store)

	// 51 chars — one over the limit
	id := "abcdefghij-klmnopqrst_uvwxyz-1234567890_abcdefghijk"
	require.Len(t, id, 51)

	body, err := json.Marshal(upsertVehicleRequest{ID: id, Label: "Test"})
	require.NoError(t, err)
	w := postVehicle(handler, body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "at most 50 characters")
}

func TestHandleUpsertVehicle_InvalidJSON(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleUpsertVehicle(store)

	w := postVehicle(handler, []byte(`{bad json`))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "invalid JSON")
}

func TestHandleUpsertVehicle_UnknownFieldRejected(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleUpsertVehicle(store)

	body := []byte(`{"id":"bus-1","label":"Bus 1","extra":"field"}`)
	w := postVehicle(handler, body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "unknown field")
}

func TestHandleUpsertVehicle_TrailingDataRejected(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleUpsertVehicle(store)

	body := []byte(`{"id":"bus-1","label":"Bus 1"}{"extra":true}`)
	w := postVehicle(handler, body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "single JSON object")
}

func TestHandleUpsertVehicle_ContentTypeRequired(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleUpsertVehicle(store)

	req := httptest.NewRequest("POST", "/api/v1/admin/vehicles", bytes.NewReader([]byte(`{"id":"bus-1"}`)))
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "Content-Type must be application/json")
}

func TestHandleUpsertVehicle_MinimalFields(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleUpsertVehicle(store)

	// Only required field is id
	body := []byte(`{"id":"bus-1"}`)
	w := postVehicle(handler, body)

	assert.Equal(t, http.StatusOK, w.Code)

	var vehicle VehicleResponse
	err := json.NewDecoder(w.Body).Decode(&vehicle)
	require.NoError(t, err)
	assert.Equal(t, "bus-1", vehicle.ID)
	assert.Equal(t, "", vehicle.Label)
	assert.Equal(t, "", vehicle.AgencyTag)
	assert.True(t, vehicle.Active)
}

func TestHandleUpsertVehicle_StoreError(t *testing.T) {
	store := newMockVehicleStore()
	store.err = errors.New("database down")

	handler := handleUpsertVehicle(store)
	body := []byte(`{"id":"bus-1"}`)
	w := postVehicle(handler, body)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "failed to save vehicle")
}

func TestHandleUpsertVehicle_BodyTooLarge(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleUpsertVehicle(store)

	// Create a body larger than 1KB
	largeBody := `{"id":"bus-1","label":"` + strings.Repeat("x", 2048) + `"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/vehicles", strings.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "request body too large")
}

func TestHandleUpsertVehicle_TypeMismatchSanitized(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleUpsertVehicle(store)

	// Send a number where a string is expected
	body := []byte(`{"id":12345}`)
	w := postVehicle(handler, body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "invalid type")
	assert.NotContains(t, resp["error"], "upsertVehicleRequest", "error should not expose internal struct name")
}

func TestHandleDeactivateVehicle_HappyPath(t *testing.T) {
	store := newMockVehicleStore()
	now := time.Now()
	store.vehicles["bus-1"] = &VehicleResponse{ID: "bus-1", Active: true, CreatedAt: now, UpdatedAt: now}

	handler := handleDeactivateVehicle(store)
	req := httptest.NewRequest("DELETE", "/api/v1/admin/vehicles/bus-1", nil)
	req.SetPathValue("id", "bus-1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.False(t, store.vehicles["bus-1"].Active)
}

func TestHandleDeactivateVehicle_NotFound(t *testing.T) {
	store := newMockVehicleStore()

	handler := handleDeactivateVehicle(store)
	req := httptest.NewRequest("DELETE", "/api/v1/admin/vehicles/unknown", nil)
	req.SetPathValue("id", "unknown")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "vehicle not found")
}

func TestHandleDeactivateVehicle_StoreError(t *testing.T) {
	store := newMockVehicleStore()
	store.err = errors.New("database down")

	handler := handleDeactivateVehicle(store)
	req := httptest.NewRequest("DELETE", "/api/v1/admin/vehicles/bus-1", nil)
	req.SetPathValue("id", "bus-1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "failed to deactivate vehicle")
}

func TestHandleDeactivateVehicle_InvalidID(t *testing.T) {
	store := newMockVehicleStore()
	handler := handleDeactivateVehicle(store)

	req := httptest.NewRequest("DELETE", "/api/v1/admin/vehicles/test", nil)
	req.SetPathValue("id", "bus@1!")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp["error"], "alphanumeric characters")
}
