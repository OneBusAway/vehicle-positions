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

// --- Mock stores ---

type mockUserLister struct {
	users []UserResponse
	err   error
}

func (m *mockUserLister) ListUsers(ctx context.Context) ([]UserResponse, error) {
	return m.users, m.err
}

type mockUserGetter struct {
	user *UserResponse
	err  error
}

func (m *mockUserGetter) GetUser(ctx context.Context, id int64) (*UserResponse, error) {
	return m.user, m.err
}

type mockUserCreator struct {
	user *UserResponse
	err  error
}

func (m *mockUserCreator) CreateUser(ctx context.Context, name, email, password, role string) (*UserResponse, error) {
	return m.user, m.err
}

type mockUserUpdater struct {
	user *UserResponse
	err  error
}

func (m *mockUserUpdater) UpdateUser(ctx context.Context, id int64, name, email, role string) (*UserResponse, error) {
	return m.user, m.err
}

type mockUserDeleter struct {
	err error
}

func (m *mockUserDeleter) DeleteUser(ctx context.Context, id int64) error {
	return m.err
}

// --- Helpers ---

func decodeErrorResponse(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	return resp["error"]
}

func newSampleUser() *UserResponse {
	return &UserResponse{
		ID:        1,
		Name:      "Alice",
		Email:     "alice@example.com",
		Role:      "admin",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// --- List Users ---

func TestHandleListUsers_Empty(t *testing.T) {
	handler := handleListUsers(&mockUserLister{users: make([]UserResponse, 0)})
	req := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Must return [] not null
	var raw json.RawMessage
	err := json.NewDecoder(w.Body).Decode(&raw)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(raw))
}

func TestHandleListUsers_WithUsers(t *testing.T) {
	users := []UserResponse{
		{ID: 1, Name: "Alice", Email: "alice@example.com", Role: "admin"},
		{ID: 2, Name: "Bob", Email: "bob@example.com", Role: "driver"},
	}
	handler := handleListUsers(&mockUserLister{users: users})
	req := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result []UserResponse
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "Alice", result[0].Name)
	assert.Equal(t, "Bob", result[1].Name)
}

func TestHandleListUsers_NoPasswordInResponse(t *testing.T) {
	users := []UserResponse{
		{ID: 1, Name: "Alice", Email: "alice@example.com", Role: "admin"},
	}
	handler := handleListUsers(&mockUserLister{users: users})
	req := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "password_hash", "password_hash must never appear in list response")
	assert.NotContains(t, w.Body.String(), "password", "password must never appear in list response")
}

func TestHandleListUsers_DBError(t *testing.T) {
	handler := handleListUsers(&mockUserLister{err: fmt.Errorf("database down")})
	req := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", decodeErrorResponse(t, w))
}

// --- Get User ---

func TestHandleGetUser_Found(t *testing.T) {
	handler := handleGetUser(&mockUserGetter{user: newSampleUser()})
	req := httptest.NewRequest("GET", "/api/v1/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result UserResponse
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "Alice", result.Name)
	assert.Equal(t, "alice@example.com", result.Email)
}

func TestHandleGetUser_NoPasswordInResponse(t *testing.T) {
	handler := handleGetUser(&mockUserGetter{user: newSampleUser()})
	req := httptest.NewRequest("GET", "/api/v1/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "password_hash", "password_hash must never appear in get response")
	assert.NotContains(t, w.Body.String(), "password", "password must never appear in get response")
}

func TestHandleGetUser_NotFound(t *testing.T) {
	handler := handleGetUser(&mockUserGetter{err: ErrUserNotFound})
	req := httptest.NewRequest("GET", "/api/v1/admin/users/999", nil)
	req.SetPathValue("id", "999")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user not found", decodeErrorResponse(t, w))
}

func TestHandleGetUser_InvalidID(t *testing.T) {
	handler := handleGetUser(&mockUserGetter{})

	tests := []struct {
		name string
		id   string
	}{
		{"non-numeric", "abc"},
		{"zero", "0"},
		{"negative", "-1"},
		{"empty", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/admin/users/"+tc.id, nil)
			req.SetPathValue("id", tc.id)
			w := httptest.NewRecorder()
			handler(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, "invalid user id", decodeErrorResponse(t, w))
		})
	}
}

func TestHandleGetUser_DBError(t *testing.T) {
	handler := handleGetUser(&mockUserGetter{err: fmt.Errorf("database down")})
	req := httptest.NewRequest("GET", "/api/v1/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", decodeErrorResponse(t, w))
}

// --- Create User ---

func TestHandleCreateUser_HappyPath(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{user: newSampleUser()})
	body := `{"name":"Alice","email":"alice@example.com","password":"securepass","role":"admin"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var result UserResponse
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "Alice", result.Name)
	assert.Equal(t, "alice@example.com", result.Email)
	assert.Equal(t, "admin", result.Role)
}

func TestHandleCreateUser_NoPasswordInResponse(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{user: newSampleUser()})
	body := `{"name":"Alice","email":"alice@example.com","password":"securepass","role":"admin"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var raw map[string]any
	err := json.NewDecoder(w.Body).Decode(&raw)
	require.NoError(t, err)
	_, hasPassword := raw["password"]
	_, hasPasswordHash := raw["password_hash"]
	assert.False(t, hasPassword, "password must not be in response")
	assert.False(t, hasPasswordHash, "password_hash must not be in response")
}

func TestHandleCreateUser_Validation(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{})

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "missing name",
			body:    `{"email":"a@b.com","password":"securepass","role":"driver"}`,
			wantErr: "name is required",
		},
		{
			name:    "missing email",
			body:    `{"name":"Alice","password":"securepass","role":"driver"}`,
			wantErr: "email is required",
		},
		{
			name:    "missing password",
			body:    `{"name":"Alice","email":"a@b.com","role":"driver"}`,
			wantErr: "password is required",
		},
		{
			name:    "short password",
			body:    `{"name":"Alice","email":"a@b.com","password":"short","role":"driver"}`,
			wantErr: "password must be at least 8 characters",
		},
		{
			name:    "invalid role",
			body:    `{"name":"Alice","email":"a@b.com","password":"securepass","role":"superadmin"}`,
			wantErr: "role must be 'driver' or 'admin'",
		},
		{
			name:    "empty role",
			body:    `{"name":"Alice","email":"a@b.com","password":"securepass","role":""}`,
			wantErr: "role must be 'driver' or 'admin'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, tc.wantErr, decodeErrorResponse(t, w))
		})
	}
}

func TestHandleCreateUser_DuplicateEmail(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{err: ErrDuplicateEmail})
	body := `{"name":"Alice","email":"alice@example.com","password":"securepass","role":"admin"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "email already exists", decodeErrorResponse(t, w))
}

func TestHandleCreateUser_WrongContentType(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{})
	body := `{"name":"Alice","email":"a@b.com","password":"securepass","role":"driver"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	assert.Equal(t, "Content-Type must be application/json", decodeErrorResponse(t, w))
}

func TestHandleCreateUser_NoContentType(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{})
	body := `{"name":"Alice","email":"a@b.com","password":"securepass","role":"driver"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	assert.Equal(t, "Content-Type must be application/json", decodeErrorResponse(t, w))
}

func TestHandleCreateUser_UnknownFieldRejected(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{})
	body := `{"name":"Alice","email":"a@b.com","password":"securepass","role":"driver","extra":"field"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeErrorResponse(t, w), "unknown field")
}

func TestHandleCreateUser_TrailingJSONRejected(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{})
	body := `{"name":"Alice","email":"a@b.com","password":"securepass","role":"driver"}{"extra":true}`
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeErrorResponse(t, w), "single JSON object")
}

func TestHandleCreateUser_EmptyBody(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{})
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte("")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeErrorResponse(t, w), "invalid JSON")
}

func TestHandleCreateUser_MalformedJSON(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{})
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte("{bad json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeErrorResponse(t, w), "invalid JSON")
}

func TestHandleCreateUser_DBError(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{err: fmt.Errorf("database down")})
	body := `{"name":"Alice","email":"alice@example.com","password":"securepass","role":"admin"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", decodeErrorResponse(t, w))
}

func TestHandleCreateUser_ContentTypeWithCharsetAccepted(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{user: newSampleUser()})
	body := `{"name":"Alice","email":"alice@example.com","password":"securepass","role":"admin"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestHandleCreateUser_BodyTooLarge(t *testing.T) {
	handler := handleCreateUser(&mockUserCreator{})
	// 1<<10 = 1024 bytes limit; send >1025 bytes
	body := `{"name":"` + strings.Repeat("A", 1100) + `","email":"a@b.com","password":"securepass","role":"driver"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/users", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeErrorResponse(t, w), "invalid JSON")
}

// --- Update User ---

func TestHandleUpdateUser_HappyPath(t *testing.T) {
	updated := &UserResponse{ID: 1, Name: "Alice Updated", Email: "alice2@example.com", Role: "driver"}
	handler := handleUpdateUser(&mockUserUpdater{user: updated})
	body := `{"name":"Alice Updated","email":"alice2@example.com","role":"driver"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/1", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result UserResponse
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "Alice Updated", result.Name)
	assert.Equal(t, "alice2@example.com", result.Email)
	assert.Equal(t, "driver", result.Role)
}

func TestHandleUpdateUser_NotFound(t *testing.T) {
	handler := handleUpdateUser(&mockUserUpdater{err: ErrUserNotFound})
	body := `{"name":"Alice","email":"a@b.com","role":"driver"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/999", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "999")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user not found", decodeErrorResponse(t, w))
}

func TestHandleUpdateUser_DuplicateEmail(t *testing.T) {
	handler := handleUpdateUser(&mockUserUpdater{err: ErrDuplicateEmail})
	body := `{"name":"Alice","email":"taken@example.com","role":"driver"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/1", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "email already exists", decodeErrorResponse(t, w))
}

func TestHandleUpdateUser_InvalidRole(t *testing.T) {
	handler := handleUpdateUser(&mockUserUpdater{})
	body := `{"name":"Alice","email":"a@b.com","role":"superadmin"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/1", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "role must be 'driver' or 'admin'", decodeErrorResponse(t, w))
}

func TestHandleUpdateUser_WrongContentType(t *testing.T) {
	handler := handleUpdateUser(&mockUserUpdater{})
	body := `{"name":"Alice","email":"a@b.com","role":"driver"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/1", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "text/plain")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, w.Code)
	assert.Equal(t, "Content-Type must be application/json", decodeErrorResponse(t, w))
}

func TestHandleUpdateUser_InvalidID(t *testing.T) {
	handler := handleUpdateUser(&mockUserUpdater{})
	body := `{"name":"Alice","email":"a@b.com","role":"driver"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/abc", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "invalid user id", decodeErrorResponse(t, w))
}

func TestHandleUpdateUser_MissingName(t *testing.T) {
	handler := handleUpdateUser(&mockUserUpdater{})
	body := `{"email":"a@b.com","role":"driver"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/1", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "name is required", decodeErrorResponse(t, w))
}

func TestHandleUpdateUser_MissingEmail(t *testing.T) {
	handler := handleUpdateUser(&mockUserUpdater{})
	body := `{"name":"Alice","role":"driver"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/1", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "email is required", decodeErrorResponse(t, w))
}

func TestHandleUpdateUser_TrailingJSONRejected(t *testing.T) {
	handler := handleUpdateUser(&mockUserUpdater{})
	body := `{"name":"Alice","email":"a@b.com","role":"driver"}{"extra":true}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/1", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeErrorResponse(t, w), "single JSON object")
}

func TestHandleUpdateUser_UnknownFieldRejected(t *testing.T) {
	handler := handleUpdateUser(&mockUserUpdater{})
	body := `{"name":"Alice","email":"a@b.com","role":"driver","password":"sneaky"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/1", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeErrorResponse(t, w), "unknown field")
}

func TestHandleUpdateUser_DBError(t *testing.T) {
	handler := handleUpdateUser(&mockUserUpdater{err: errors.New("database down")})
	body := `{"name":"Alice","email":"a@b.com","role":"driver"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/1", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", decodeErrorResponse(t, w))
}

func TestHandleUpdateUser_BodyTooLarge(t *testing.T) {
	handler := handleUpdateUser(&mockUserUpdater{})
	body := `{"name":"` + strings.Repeat("A", 1100) + `","email":"a@b.com","role":"driver"}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/users/1", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decodeErrorResponse(t, w), "invalid JSON")
}

// --- Delete User ---

func TestHandleDeleteUser_HappyPath(t *testing.T) {
	handler := handleDeleteUser(&mockUserDeleter{})
	req := httptest.NewRequest("DELETE", "/api/v1/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String(), "204 No Content must have no response body")
}

func TestHandleDeleteUser_NotFound(t *testing.T) {
	handler := handleDeleteUser(&mockUserDeleter{err: ErrUserNotFound})
	req := httptest.NewRequest("DELETE", "/api/v1/admin/users/999", nil)
	req.SetPathValue("id", "999")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "user not found", decodeErrorResponse(t, w))
}

func TestHandleDeleteUser_InvalidID(t *testing.T) {
	handler := handleDeleteUser(&mockUserDeleter{})
	req := httptest.NewRequest("DELETE", "/api/v1/admin/users/abc", nil)
	req.SetPathValue("id", "abc")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "invalid user id", decodeErrorResponse(t, w))
}

func TestHandleDeleteUser_DBError(t *testing.T) {
	handler := handleDeleteUser(&mockUserDeleter{err: fmt.Errorf("database down")})
	req := httptest.NewRequest("DELETE", "/api/v1/admin/users/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", decodeErrorResponse(t, w))
}
