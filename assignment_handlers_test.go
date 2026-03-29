package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock implementations ---

type mockAssignmentCreator struct {
	assignment *AssignmentResponse
	err        error
}

func (m *mockAssignmentCreator) CreateAssignment(ctx context.Context, userID int64, vehicleID string) (*AssignmentResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.assignment, nil
}

type mockAssignmentDeleter struct {
	err error
}

func (m *mockAssignmentDeleter) DeleteAssignment(ctx context.Context, userID int64, vehicleID string) error {
	return m.err
}

type mockAssignmentListerByUser struct {
	assignments []AssignmentResponse
	err         error
}

func (m *mockAssignmentListerByUser) ListAssignmentsByUser(ctx context.Context, userID int64) ([]AssignmentResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.assignments, nil
}

type mockAssignmentListerByVehicle struct {
	assignments []AssignmentResponse
	err         error
}

func (m *mockAssignmentListerByVehicle) ListAssignmentsByVehicle(ctx context.Context, vehicleID string) ([]AssignmentResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.assignments, nil
}

// --- helpers ---

func postAssignment(handler http.HandlerFunc, body []byte, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/v1/admin/assignments", bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func deleteAssignment(handler http.HandlerFunc, userID, vehicleID string) *httptest.ResponseRecorder {
	path := fmt.Sprintf("/api/v1/admin/users/%s/vehicles/%s", userID, vehicleID)
	req := httptest.NewRequest("DELETE", path, nil)
	req.SetPathValue("userID", userID)
	req.SetPathValue("vehicleID", vehicleID)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func listUserVehicles(handler http.HandlerFunc, userID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/v1/admin/users/"+userID+"/vehicles", nil)
	req.SetPathValue("id", userID)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func listVehicleUsers(handler http.HandlerFunc, vehicleID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/v1/admin/vehicles/"+vehicleID+"/users", nil)
	req.SetPathValue("id", vehicleID)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func decodeAssignmentError(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	return resp["error"]
}

// --- Create Assignment tests ---

func TestHandleCreateAssignment_Success(t *testing.T) {
	now := time.Now()
	mock := &mockAssignmentCreator{
		assignment: &AssignmentResponse{UserID: 1, VehicleID: "bus-1", CreatedAt: now},
	}
	handler := handleCreateAssignment(mock)

	body, _ := json.Marshal(AssignmentRequest{UserID: 1, VehicleID: "bus-1"})
	w := postAssignment(handler, body, "application/json")

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp AssignmentResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.UserID)
	assert.Equal(t, "bus-1", resp.VehicleID)
	assert.False(t, resp.CreatedAt.IsZero())
}

func TestHandleCreateAssignment_Validation(t *testing.T) {
	mock := &mockAssignmentCreator{}
	handler := handleCreateAssignment(mock)

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing user_id",
			body:       `{"vehicle_id": "bus-1"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "user_id is required and must be positive",
		},
		{
			name:       "negative user_id",
			body:       `{"user_id": -1, "vehicle_id": "bus-1"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "user_id is required and must be positive",
		},
		{
			name:       "zero user_id",
			body:       `{"user_id": 0, "vehicle_id": "bus-1"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "user_id is required and must be positive",
		},
		{
			name:       "missing vehicle_id",
			body:       `{"user_id": 1}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "vehicle id is required",
		},
		{
			name:       "empty vehicle_id",
			body:       `{"user_id": 1, "vehicle_id": ""}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "vehicle id is required",
		},
		{
			name:       "vehicle_id with spaces",
			body:       `{"user_id": 1, "vehicle_id": "bus 1"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "vehicle id must contain only alphanumeric characters, dots, hyphens, and underscores",
		},
		{
			name:       "vehicle_id with special chars",
			body:       `{"user_id": 1, "vehicle_id": "bus@1!"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "vehicle id must contain only alphanumeric characters, dots, hyphens, and underscores",
		},
		{
			name:       "vehicle_id too long 51 chars",
			body:       fmt.Sprintf(`{"user_id": 1, "vehicle_id": "%s"}`, strings.Repeat("a", 51)),
			wantStatus: http.StatusBadRequest,
			wantError:  "vehicle id must be at most 50 characters",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := postAssignment(handler, []byte(tc.body), "application/json")
			assert.Equal(t, tc.wantStatus, w.Code)
			errMsg := decodeAssignmentError(t, w)
			assert.Contains(t, errMsg, tc.wantError)
		})
	}
}

func TestHandleCreateAssignment_VehicleID50CharsAccepted(t *testing.T) {
	now := time.Now()
	vehicleID := strings.Repeat("a", 50)
	mock := &mockAssignmentCreator{
		assignment: &AssignmentResponse{UserID: 1, VehicleID: vehicleID, CreatedAt: now},
	}
	handler := handleCreateAssignment(mock)

	body := fmt.Sprintf(`{"user_id": 1, "vehicle_id": "%s"}`, vehicleID)
	w := postAssignment(handler, []byte(body), "application/json")

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestHandleCreateAssignment_DuplicateAssignment(t *testing.T) {
	mock := &mockAssignmentCreator{err: ErrAssignmentExists}
	handler := handleCreateAssignment(mock)

	body, _ := json.Marshal(AssignmentRequest{UserID: 1, VehicleID: "bus-1"})
	w := postAssignment(handler, body, "application/json")

	assert.Equal(t, http.StatusConflict, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Equal(t, "assignment already exists", errMsg)
}

func TestHandleCreateAssignment_UserNotFound(t *testing.T) {
	mock := &mockAssignmentCreator{err: ErrUserNotFoundFK}
	handler := handleCreateAssignment(mock)

	body, _ := json.Marshal(AssignmentRequest{UserID: 999, VehicleID: "bus-1"})
	w := postAssignment(handler, body, "application/json")

	assert.Equal(t, http.StatusNotFound, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Equal(t, "user not found", errMsg)
}

func TestHandleCreateAssignment_VehicleNotFound(t *testing.T) {
	mock := &mockAssignmentCreator{err: ErrVehicleNotFoundFK}
	handler := handleCreateAssignment(mock)

	body, _ := json.Marshal(AssignmentRequest{UserID: 1, VehicleID: "nonexistent"})
	w := postAssignment(handler, body, "application/json")

	assert.Equal(t, http.StatusNotFound, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Equal(t, "vehicle not found", errMsg)
}

func TestHandleCreateAssignment_DBError(t *testing.T) {
	mock := &mockAssignmentCreator{err: errors.New("database down")}
	handler := handleCreateAssignment(mock)

	body, _ := json.Marshal(AssignmentRequest{UserID: 1, VehicleID: "bus-1"})
	w := postAssignment(handler, body, "application/json")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Equal(t, "internal server error", errMsg)
}

func TestHandleCreateAssignment_WrongContentType(t *testing.T) {
	handler := handleCreateAssignment(&mockAssignmentCreator{})

	w := postAssignment(handler, []byte(`{}`), "text/plain")
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Contains(t, errMsg, "Content-Type must be application/json")
}

func TestHandleCreateAssignment_MissingContentType(t *testing.T) {
	handler := handleCreateAssignment(&mockAssignmentCreator{})

	w := postAssignment(handler, []byte(`{}`), "")
	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
}

func TestHandleCreateAssignment_UnknownFieldRejected(t *testing.T) {
	handler := handleCreateAssignment(&mockAssignmentCreator{})

	body := []byte(`{"user_id": 1, "vehicle_id": "bus-1", "extra": true}`)
	w := postAssignment(handler, body, "application/json")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Contains(t, errMsg, "unknown field")
}

func TestHandleCreateAssignment_TrailingDataRejected(t *testing.T) {
	handler := handleCreateAssignment(&mockAssignmentCreator{})

	body := []byte(`{"user_id": 1, "vehicle_id": "bus-1"}{"extra": true}`)
	w := postAssignment(handler, body, "application/json")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Contains(t, errMsg, "single JSON object")
}

func TestHandleCreateAssignment_TrailingGarbageRejected(t *testing.T) {
	handler := handleCreateAssignment(&mockAssignmentCreator{})

	body := []byte(`{"user_id": 1, "vehicle_id": "bus-1"}GARBAGE`)
	w := postAssignment(handler, body, "application/json")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Contains(t, errMsg, "invalid JSON:")
}

func TestHandleCreateAssignment_EmptyBody(t *testing.T) {
	handler := handleCreateAssignment(&mockAssignmentCreator{})

	w := postAssignment(handler, []byte(``), "application/json")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Contains(t, errMsg, "invalid JSON:")
}

func TestHandleCreateAssignment_InvalidJSON(t *testing.T) {
	handler := handleCreateAssignment(&mockAssignmentCreator{})

	w := postAssignment(handler, []byte(`{bad json`), "application/json")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Contains(t, errMsg, "invalid JSON:")
}

func TestHandleCreateAssignment_ContentTypeWithCharset(t *testing.T) {
	now := time.Now()
	mock := &mockAssignmentCreator{
		assignment: &AssignmentResponse{UserID: 1, VehicleID: "bus-1", CreatedAt: now},
	}
	handler := handleCreateAssignment(mock)

	body, _ := json.Marshal(AssignmentRequest{UserID: 1, VehicleID: "bus-1"})
	w := postAssignment(handler, body, "application/json; charset=utf-8")

	assert.Equal(t, http.StatusCreated, w.Code)
}

// --- Delete Assignment tests ---

func TestHandleDeleteAssignment_Success(t *testing.T) {
	mock := &mockAssignmentDeleter{}
	handler := handleDeleteAssignment(mock)

	w := deleteAssignment(handler, "1", "bus-1")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "assignment removed", resp["status"])
}

func TestHandleDeleteAssignment_NotFound(t *testing.T) {
	mock := &mockAssignmentDeleter{err: ErrAssignmentNotFound}
	handler := handleDeleteAssignment(mock)

	w := deleteAssignment(handler, "1", "bus-1")

	assert.Equal(t, http.StatusNotFound, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Equal(t, "assignment not found", errMsg)
}

func TestHandleDeleteAssignment_InvalidUserID(t *testing.T) {
	handler := handleDeleteAssignment(&mockAssignmentDeleter{})

	tests := []struct {
		name   string
		userID string
	}{
		{"non-numeric", "abc"},
		{"negative", "-1"},
		{"zero", "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := deleteAssignment(handler, tc.userID, "bus-1")
			assert.Equal(t, http.StatusBadRequest, w.Code)
			errMsg := decodeAssignmentError(t, w)
			assert.Equal(t, "invalid user id", errMsg)
		})
	}
}

func TestHandleDeleteAssignment_InvalidVehicleID(t *testing.T) {
	handler := handleDeleteAssignment(&mockAssignmentDeleter{})

	tests := []struct {
		name      string
		vehicleID string
	}{
		{"special chars", "bus@1!"},
		{"too long", strings.Repeat("a", 51)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := deleteAssignment(handler, "1", tc.vehicleID)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			errMsg := decodeAssignmentError(t, w)
			assert.Equal(t, "invalid vehicle id", errMsg)
		})
	}
}

func TestHandleDeleteAssignment_DBError(t *testing.T) {
	mock := &mockAssignmentDeleter{err: errors.New("database down")}
	handler := handleDeleteAssignment(mock)

	w := deleteAssignment(handler, "1", "bus-1")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Equal(t, "internal server error", errMsg)
}

// --- List User Vehicles tests ---

func TestHandleListUserVehicles_Empty(t *testing.T) {
	mock := &mockAssignmentListerByUser{assignments: make([]AssignmentResponse, 0)}
	handler := handleListUserVehicles(mock)

	w := listUserVehicles(handler, "1")

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify JSON is [] not null
	body := strings.TrimSpace(w.Body.String())
	assert.Equal(t, "[]", body)
}

func TestHandleListUserVehicles_WithAssignments(t *testing.T) {
	now := time.Now()
	mock := &mockAssignmentListerByUser{
		assignments: []AssignmentResponse{
			{UserID: 1, VehicleID: "bus-1", CreatedAt: now},
			{UserID: 1, VehicleID: "bus-2", CreatedAt: now},
		},
	}
	handler := handleListUserVehicles(mock)

	w := listUserVehicles(handler, "1")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []AssignmentResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Len(t, resp, 2)
	assert.Equal(t, "bus-1", resp[0].VehicleID)
	assert.Equal(t, "bus-2", resp[1].VehicleID)
}

func TestHandleListUserVehicles_InvalidUserID(t *testing.T) {
	handler := handleListUserVehicles(&mockAssignmentListerByUser{})

	tests := []struct {
		name   string
		userID string
	}{
		{"non-numeric", "abc"},
		{"negative", "-1"},
		{"zero", "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := listUserVehicles(handler, tc.userID)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			errMsg := decodeAssignmentError(t, w)
			assert.Equal(t, "invalid user id", errMsg)
		})
	}
}

func TestHandleListUserVehicles_DBError(t *testing.T) {
	mock := &mockAssignmentListerByUser{err: errors.New("database down")}
	handler := handleListUserVehicles(mock)

	w := listUserVehicles(handler, "1")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Equal(t, "internal server error", errMsg)
}

// --- List Vehicle Users tests ---

func TestHandleListVehicleUsers_Empty(t *testing.T) {
	mock := &mockAssignmentListerByVehicle{assignments: make([]AssignmentResponse, 0)}
	handler := handleListVehicleUsers(mock)

	w := listVehicleUsers(handler, "bus-1")

	assert.Equal(t, http.StatusOK, w.Code)

	body := strings.TrimSpace(w.Body.String())
	assert.Equal(t, "[]", body)
}

func TestHandleListVehicleUsers_WithAssignments(t *testing.T) {
	now := time.Now()
	mock := &mockAssignmentListerByVehicle{
		assignments: []AssignmentResponse{
			{UserID: 1, VehicleID: "bus-1", CreatedAt: now},
			{UserID: 2, VehicleID: "bus-1", CreatedAt: now},
		},
	}
	handler := handleListVehicleUsers(mock)

	w := listVehicleUsers(handler, "bus-1")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []AssignmentResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Len(t, resp, 2)
	assert.Equal(t, int64(1), resp[0].UserID)
	assert.Equal(t, int64(2), resp[1].UserID)
}

func TestHandleListVehicleUsers_InvalidVehicleID(t *testing.T) {
	handler := handleListVehicleUsers(&mockAssignmentListerByVehicle{})

	tests := []struct {
		name      string
		vehicleID string
	}{
		{"special chars", "bus@1!"},
		{"too long", strings.Repeat("a", 51)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := listVehicleUsers(handler, tc.vehicleID)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			errMsg := decodeAssignmentError(t, w)
			assert.Equal(t, "invalid vehicle id", errMsg)
		})
	}
}

func TestHandleListVehicleUsers_DBError(t *testing.T) {
	mock := &mockAssignmentListerByVehicle{err: errors.New("database down")}
	handler := handleListVehicleUsers(mock)

	w := listVehicleUsers(handler, "bus-1")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	errMsg := decodeAssignmentError(t, w)
	assert.Equal(t, "internal server error", errMsg)
}
