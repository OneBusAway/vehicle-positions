package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockUserStore struct {
	user             *User
	err              error
	createRefreshErr error
}

var testSecret = []byte("super-secret-test-key-32-bytes!!")

func (m *mockUserStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.user, nil
}

func (m *mockUserStore) CreateRefreshToken(_ context.Context, _ string, _ int64, _ string, _, _ time.Time) error {
	return m.createRefreshErr
}

func postLogin(handler http.HandlerFunc, email, password string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(LoginRequest{Email: email, Password: password})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandleLogin_Success(t *testing.T) {
	store := &mockUserStore{user: &User{
		ID:           1,
		Email:        "driver@test.com",
		PasswordHash: "$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi",
		Role:         "driver",
	}}

	handler := handleLogin(store, testSecret, 15*time.Minute, 7*24*time.Hour)
	w := postLogin(handler, "driver@test.com", "password")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp LoginResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
}

func TestHandleLogin_WrongPassword(t *testing.T) {
	store := &mockUserStore{user: &User{
		ID:           1,
		Email:        "driver@test.com",
		PasswordHash: "$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi",
		Role:         "driver",
	}}

	handler := handleLogin(store, testSecret, 15*time.Minute, 7*24*time.Hour)
	w := postLogin(handler, "driver@test.com", "wrongpassword")

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid email or password", resp["error"])
}

func TestHandleLogin_UserNotFound(t *testing.T) {
	store := &mockUserStore{err: pgx.ErrNoRows}

	handler := handleLogin(store, testSecret, 15*time.Minute, 7*24*time.Hour)
	w := postLogin(handler, "nobody@test.com", "password")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleLogin_MissingFields(t *testing.T) {
	store := &mockUserStore{}
	handler := handleLogin(store, testSecret, 15*time.Minute, 7*24*time.Hour)

	tests := []struct {
		name     string
		email    string
		password string
	}{
		{"missing email", "", "password"},
		{"missing password", "driver@test.com", ""},
		{"missing both", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := postLogin(handler, tc.email, tc.password)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestHandleLogin_InvalidJSON(t *testing.T) {
	store := &mockUserStore{}
	handler := handleLogin(store, testSecret, 15*time.Minute, 7*24*time.Hour)

	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader([]byte("{bad json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func dummyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireAuth_MissingHeader(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/locations", nil)
	w := httptest.NewRecorder()

	requireAuth(testSecret)(dummyHandler()).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_MalformedHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"no bearer prefix", "sometoken"},
		{"wrong scheme", "Basic sometoken"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/locations", nil)
			req.Header.Set("Authorization", tc.header)
			w := httptest.NewRecorder()

			requireAuth(testSecret)(dummyHandler()).ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer notavalidtoken")
	w := httptest.NewRecorder()

	requireAuth(testSecret)(dummyHandler()).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_ExpiredToken(t *testing.T) {
	claims := jwt.MapClaims{
		"sub": 1,
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := token.SignedString(testSecret)

	req := httptest.NewRequest("POST", "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rr := httptest.NewRecorder()

	middleware := requireAuth(testSecret)
	handler := middleware(dummyHandler())

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireAuth_ValidToken(t *testing.T) {
	token, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret, 15*time.Minute)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(claimsKey).(jwt.MapClaims)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		assert.Equal(t, "driver@test.com", claims["email"])
		w.WriteHeader(http.StatusOK)
	})

	requireAuth(testSecret)(handler).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGenerateJWT_Claims(t *testing.T) {
	user := &User{ID: 42, Email: "driver@transit.com", Role: "driver"}

	tokenStr, err := generateJWT(user, testSecret, 15*time.Minute)
	require.NoError(t, err)

	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return testSecret, nil
	})
	require.NoError(t, err)

	claims, ok := token.Claims.(jwt.MapClaims)
	require.True(t, ok)

	assert.Equal(t, "42", claims["sub"])
	assert.Equal(t, "driver@transit.com", claims["email"])
	assert.Equal(t, "driver", claims["role"])
	assert.Equal(t, "vehicle-positions-api", claims["iss"])

	exp, ok := claims["exp"].(float64)
	require.True(t, ok)
	assert.True(t, exp > float64(time.Now().Unix()))
}

func TestRequireAuth_WrongSecret(t *testing.T) {
	wrongSecret := []byte("the-wrong-secret-key-32-bytes-!!")

	user := &User{ID: 1, Email: "hacker@evil.com", Role: "admin"}
	tokenStr, _ := generateJWT(user, wrongSecret, 15*time.Minute)

	req := httptest.NewRequest("POST", "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rr := httptest.NewRecorder()

	middleware := requireAuth(testSecret)
	handler := middleware(dummyHandler())

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireAuth_AlgorithmConfusion(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":1}`))

	tokenStr := header + "." + payload + "."

	req := httptest.NewRequest("POST", "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rr := httptest.NewRecorder()

	middleware := requireAuth(testSecret)
	handler := middleware(dummyHandler())

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

type mockRefreshStore struct {
	token   *RefreshToken
	getErr  error
	user    *User
	userErr error
}

func (m *mockRefreshStore) GetRefreshToken(_ context.Context, _ string) (*RefreshToken, error) {
	return m.token, m.getErr
}

func (m *mockRefreshStore) GetUserByID(_ context.Context, _ int64) (*User, error) {
	return m.user, m.userErr
}

func postRefresh(handler http.HandlerFunc, refreshToken string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(RefreshRequest{RefreshToken: refreshToken})
	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandleRefreshToken_Success(t *testing.T) {
	user := &User{ID: 1, Email: "driver@test.com", Role: "driver"}
	store := &mockRefreshStore{
		token: &RefreshToken{
			UserID:    1,
			ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
			Revoked:   false,
		},
		user: user,
	}

	handler := handleRefreshToken(store, testSecret, 15*time.Minute)
	w := postRefresh(handler, "somevalidtoken")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp RefreshResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.AccessToken)
}

func TestHandleRefreshToken_RevokedToken(t *testing.T) {
	store := &mockRefreshStore{
		token: &RefreshToken{
			UserID:    1,
			ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
			Revoked:   true,
		},
	}

	handler := handleRefreshToken(store, testSecret, 15*time.Minute)
	w := postRefresh(handler, "sometoken")

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "refresh token has been revoked", resp["error"])
}

func TestHandleRefreshToken_ExpiredToken(t *testing.T) {
	store := &mockRefreshStore{
		token: &RefreshToken{
			UserID:    1,
			ExpiresAt: time.Now().Add(-1 * time.Hour),
			Revoked:   false,
		},
	}

	handler := handleRefreshToken(store, testSecret, 15*time.Minute)
	w := postRefresh(handler, "sometoken")

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "refresh token has expired", resp["error"])
}

func TestHandleRefreshToken_TokenNotFound(t *testing.T) {
	store := &mockRefreshStore{getErr: pgx.ErrNoRows}

	handler := handleRefreshToken(store, testSecret, 15*time.Minute)
	w := postRefresh(handler, "unknowntoken")

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid refresh token", resp["error"])
}
