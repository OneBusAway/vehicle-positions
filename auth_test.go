package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// mockUserStore implements UserFetcher for tests.
type mockUserStore struct {
	user *User
	err  error
}

var testSecret = []byte("super-secret-test-key-32-bytes!!")

func (m *mockUserStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.user, nil
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
		Active:       true,
	}}

	handler := handleLogin(store, testSecret, nil, false)
	w := postLogin(handler, "driver@test.com", "password")

	assert.Equal(t, http.StatusOK, w.Code)

	var resp LoginResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Token)
}

func TestHandleLogin_WrongPassword(t *testing.T) {
	store := &mockUserStore{user: &User{
		ID:           1,
		Email:        "driver@test.com",
		PasswordHash: "$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi",
		Role:         "driver",
		Active:       true,
	}}

	handler := handleLogin(store, testSecret, nil, false)
	w := postLogin(handler, "driver@test.com", "wrongpassword")

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid email or password", resp["error"])
}

func TestHandleLogin_DeactivatedUser(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcryptCost)
	require.NoError(t, err)
	store := &mockUserStore{user: &User{
		ID:           7,
		Email:        "gone@test.com",
		PasswordHash: string(hash),
		Role:         "driver",
		Active:       false,
	}}

	handler := handleLogin(store, testSecret, nil, false)
	w := postLogin(handler, "gone@test.com", "password123")

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]string
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid email or password", resp["error"])
}

func TestHandleLogin_UserNotFound(t *testing.T) {
	store := &mockUserStore{err: ErrUserNotFound}

	handler := handleLogin(store, testSecret, nil, false)
	w := postLogin(handler, "nobody@test.com", "password")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleLogin_MissingFields(t *testing.T) {
	store := &mockUserStore{}
	handler := handleLogin(store, testSecret, nil, false)

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

// TestHandleLogin_RateLimited verifies the limiter is checked before the
// store is touched: once the per-email window is exhausted, further attempts
// get 429 even against a store that would otherwise succeed.
func TestHandleLogin_RateLimited(t *testing.T) {
	store := &mockUserStore{user: &User{
		ID:           1,
		Email:        "driver@test.com",
		PasswordHash: "$2a$10$92IXUNpkjO0rOQ5byMi.Ye4oKoEa3Ro9llC/.og/at2.uheWG/igi",
		Role:         "driver",
		Active:       true,
	}}
	limiter := NewLoginRateLimiter()
	defer limiter.Stop()

	handler := handleLogin(store, testSecret, limiter, false)

	for range loginEmailLimit {
		w := postLogin(handler, "driver@test.com", "wrongpassword")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	}

	w := postLogin(handler, "driver@test.com", "password")
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "too many attempts", resp["error"])
}

func TestHandleLogin_InvalidJSON(t *testing.T) {
	store := &mockUserStore{}
	handler := handleLogin(store, testSecret, nil, false)

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

	requireAuth(testSecret, newFakeRevocations())(dummyHandler()).ServeHTTP(w, req)

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

			requireAuth(testSecret, newFakeRevocations())(dummyHandler()).ServeHTTP(w, req)

			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer notavalidtoken")
	w := httptest.NewRecorder()

	requireAuth(testSecret, newFakeRevocations())(dummyHandler()).ServeHTTP(w, req)

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

	middleware := requireAuth(testSecret, newFakeRevocations())
	handler := middleware(dummyHandler())

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireAuth_ValidToken(t *testing.T) {
	token, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret)
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

	requireAuth(testSecret, newFakeRevocations())(handler).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAuthCookieFallback(t *testing.T) {
	token, err := generateJWT(&User{ID: 3, Email: "admin@test.com", Role: "admin", Active: true}, testSecret)
	require.NoError(t, err)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := requireAuth(testSecret, newFakeRevocations())(next)

	t.Run("cookie only → 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
	t.Run("invalid bearer + valid cookie → 401, no fallback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer garbage")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
	t.Run("malformed header + valid cookie → 401, no fallback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Basic abc")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
	t.Run("bad cookie only → 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "garbage"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestGenerateJWT_Claims(t *testing.T) {
	user := &User{ID: 42, Email: "driver@transit.com", Role: "driver"}

	tokenStr, err := generateJWT(user, testSecret)
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
	tokenStr, _ := generateJWT(user, wrongSecret)

	req := httptest.NewRequest("POST", "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	rr := httptest.NewRecorder()

	middleware := requireAuth(testSecret, newFakeRevocations())
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

	middleware := requireAuth(testSecret, newFakeRevocations())
	handler := middleware(dummyHandler())

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireAdmin_AdminAllowed(t *testing.T) {
	token, err := generateJWT(&User{ID: 1, Email: "admin@test.com", Role: "admin"}, testSecret)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/v1/admin/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	var receivedRole string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(claimsKey).(jwt.MapClaims)
		require.True(t, ok)
		receivedRole, _ = claims["role"].(string)
		w.WriteHeader(http.StatusOK)
	})

	requireAuth(testSecret, newFakeRevocations())(requireAdmin()(handler)).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "admin", receivedRole)
}

func TestRequireAdmin_DriverDenied(t *testing.T) {
	token, err := generateJWT(&User{ID: 2, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/v1/admin/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	requireAuth(testSecret, newFakeRevocations())(requireAdmin()(dummyHandler())).ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)

	var resp map[string]string
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "admin access required", resp["error"])
}

func TestRequireAdmin_MissingClaims(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/admin/status", nil)
	w := httptest.NewRecorder()

	requireAdmin()(dummyHandler()).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]string
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "unauthorized", resp["error"])
}

func TestRequireAdmin_EmptyRole(t *testing.T) {
	token, err := generateJWT(&User{ID: 3, Email: "empty@test.com", Role: ""}, testSecret)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/v1/admin/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	// Over the real chain requireAuth now rejects any role outside
	// driver/admin at the door, so the token never reaches requireAdmin.
	requireAuth(testSecret, newFakeRevocations())(requireAdmin()(dummyHandler())).ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "forbidden", decodeError(t, w))

	// requireAdmin itself still denies an empty role when reached directly.
	w = httptest.NewRecorder()
	req = req.WithContext(contextWithClaims(req.Context(), jwt.MapClaims{"sub": "3", "role": ""}))
	requireAdmin()(dummyHandler()).ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "admin access required", decodeError(t, w))
}

func TestRequireAdmin_InvalidRoleType(t *testing.T) {
	// Manually craft JWT with role as a number instead of string
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   "99",
		"email": "bad@test.com",
		"role":  123,
		"exp":   now.Add(24 * time.Hour).Unix(),
		"iat":   now.Unix(),
		"iss":   "vehicle-positions-api",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString(testSecret)
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/v1/admin/status", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()

	// A non-string role reads as "" to requireAuth's role check, so the
	// request is denied at the door before requireAdmin runs.
	requireAuth(testSecret, newFakeRevocations())(requireAdmin()(dummyHandler())).ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "forbidden", decodeError(t, w))

	// requireAdmin itself still denies a non-string role when reached directly.
	w = httptest.NewRecorder()
	req = req.WithContext(contextWithClaims(req.Context(), claims))
	requireAdmin()(dummyHandler()).ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "admin access required", decodeError(t, w))
}

func TestRequireAdmin_NoAuthHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/admin/status", nil)
	w := httptest.NewRecorder()

	requireAuth(testSecret, newFakeRevocations())(requireAdmin()(dummyHandler())).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_RejectsRiderRole(t *testing.T) {
	riderTok, _ := generateRiderJWT("rider-1", testSecret, time.Hour)
	called := false
	h := requireAuth(testSecret, newFakeRevocations())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+riderTok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, called)
	assert.Equal(t, "forbidden", decodeError(t, w))
}

// fakeRevocations is an in-memory TokenChecker/TokenRevoker for middleware and
// handler tests. Setting err makes both methods fail, which exercises the
// fail-closed paths.
type fakeRevocations struct {
	revoked       map[string]struct{}
	err           error
	lastUserID    int64
	lastExpiresAt time.Time
	revokeCalls   int
}

func newFakeRevocations() *fakeRevocations {
	return &fakeRevocations{revoked: make(map[string]struct{})}
}

func (f *fakeRevocations) IsTokenRevoked(_ context.Context, jti string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	_, ok := f.revoked[jti]
	return ok, nil
}

func (f *fakeRevocations) RevokeToken(_ context.Context, jti string, userID int64, expiresAt time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.revokeCalls++
	f.lastUserID = userID
	f.lastExpiresAt = expiresAt
	f.revoked[jti] = struct{}{}
	return nil
}

var _ TokenChecker = (*fakeRevocations)(nil)
var _ TokenRevoker = (*fakeRevocations)(nil)

// jtiOf extracts the jti claim from a signed token.
func jtiOf(t *testing.T, tokenStr string) string {
	t.Helper()
	claims, err := parseSessionToken(tokenStr, testSecret)
	require.NoError(t, err)
	jti, ok := claims["jti"].(string)
	require.True(t, ok, "token must carry a string jti")
	require.NotEmpty(t, jti)
	return jti
}

// errorBody decodes a JSON error response and returns its "error" field.
func errorBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp["error"]
}

func TestGenerateJWT_IncludesJti(t *testing.T) {
	tokenStr, err := generateJWT(&User{ID: 7, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)

	claims, err := parseSessionToken(tokenStr, testSecret)
	require.NoError(t, err)

	jti, ok := claims["jti"].(string)
	require.True(t, ok, "jti must be present and a string")
	assert.NotEmpty(t, jti)
	assert.Len(t, jti, 32, "128 random bits, hex-encoded")
}

func TestGenerateJWT_JtiIsUnique(t *testing.T) {
	user := &User{ID: 7, Email: "driver@test.com", Role: "driver"}

	first, err := generateJWT(user, testSecret)
	require.NoError(t, err)
	second, err := generateJWT(user, testSecret)
	require.NoError(t, err)

	assert.NotEqual(t, jtiOf(t, first), jtiOf(t, second),
		"each token needs its own identifier, or revoking one revokes them all")
}

func TestNewJTI_Unique(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		jti, err := newJTI()
		require.NoError(t, err)
		require.NotEmpty(t, jti)
		_, dup := seen[jti]
		require.False(t, dup, "newJTI must not repeat")
		seen[jti] = struct{}{}
	}
	assert.Len(t, seen, 100)
}

func TestRequireAuth_RejectsRevokedToken(t *testing.T) {
	token, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)

	revocations := newFakeRevocations()
	revocations.revoked[jtiOf(t, token)] = struct{}{}

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	requireAuth(testSecret, revocations)(next).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "invalid token", errorBody(t, w),
		"a revoked token must be indistinguishable from a malformed one")
	assert.False(t, reached, "the downstream handler must not run")
}

func TestRequireAuth_AllowsUnrevokedToken(t *testing.T) {
	token, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)

	// A different token is revoked: the check must be per-jti, not per-user.
	other, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)
	revocations := newFakeRevocations()
	revocations.revoked[jtiOf(t, other)] = struct{}{}

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		claims, ok := r.Context().Value(claimsKey).(jwt.MapClaims)
		require.True(t, ok, "claims must reach the downstream handler")
		assert.Equal(t, jtiOf(t, token), claims["jti"])
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	requireAuth(testSecret, revocations)(next).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, reached, "the downstream handler must run")
}

// TestRequireAuth_AllowsTokenWithoutJti covers the backwards-compatibility
// shim in checkRevoked: tokens issued before jti existed are still accepted
// (they just can't be revoked), and doing so is logged.
// Not safe for t.Parallel(); uses global logger.
func TestRequireAuth_AllowsTokenWithoutJti(t *testing.T) {
	claims := jwt.MapClaims{
		"sub":   "1",
		"email": "driver@test.com",
		"role":  "driver",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
		"iss":   "vehicle-positions-api",
	}
	tokenStr, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(testSecret)
	require.NoError(t, err)

	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(original) })

	req := httptest.NewRequest(http.MethodPost, "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	requireAuth(testSecret, newFakeRevocations())(dummyHandler()).ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "a pre-jti token must not be locked out")
	assert.Contains(t, logs.String(), "accepted token without jti",
		"accepting an unrevokable token must be logged")
}

func TestRequireAuth_CheckerErrorFailsClosed(t *testing.T) {
	token, err := generateJWT(&User{ID: 1, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)

	revocations := newFakeRevocations()
	revocations.err = errors.New("database unavailable")

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	requireAuth(testSecret, revocations)(next).ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusOK, w.Code, "an undecidable revocation check must not allow the request")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", errorBody(t, w))
	assert.False(t, reached, "the downstream handler must not run")
}

// TestRequireAuth_RejectsTokenWithoutExp pins the tightened parseSessionToken:
// a signed token with no exp is not one generateJWT issued, and a revocation
// row could not record an expiry for it.
func TestRequireAuth_RejectsTokenWithoutExp(t *testing.T) {
	claims := jwt.MapClaims{
		"sub":   "1",
		"email": "driver@test.com",
		"role":  "driver",
		"jti":   "abc123",
		"iat":   time.Now().Unix(),
		"iss":   "vehicle-positions-api",
	}
	tokenStr, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(testSecret)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/locations", nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	w := httptest.NewRecorder()
	requireAuth(testSecret, newFakeRevocations())(dummyHandler()).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "invalid token", errorBody(t, w))
}

// TestRequireAuthCookiePath_RejectsRevokedToken is the divergence guard for
// requireAuth's cookie fallback: the admin UI's vp_session cookie carries the
// same JWT as the Authorization header, so revocation must be enforced no
// matter which one delivers it.
func TestRequireAuthCookiePath_RejectsRevokedToken(t *testing.T) {
	token, err := generateJWT(&User{ID: 3, Email: "admin@test.com", Role: "admin", Active: true}, testSecret)
	require.NoError(t, err)

	revocations := newFakeRevocations()
	revocations.revoked[jtiOf(t, token)] = struct{}{}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	requireAuth(testSecret, revocations)(dummyHandler()).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "invalid token", errorBody(t, w))
}

// logoutRequest builds an authenticated logout request whose context carries
// the claims requireAuth would have put there.
func logoutRequest(t *testing.T, tokenStr string) *http.Request {
	t.Helper()
	claims, err := parseSessionToken(tokenStr, testSecret)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	return req.WithContext(contextWithClaims(req.Context(), claims))
}

func TestHandleLogout_Returns204(t *testing.T) {
	token, err := generateJWT(&User{ID: 5, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	handleLogout(newFakeRevocations())(w, logoutRequest(t, token))

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String(), "204 means no body")
}

func TestHandleLogout_RevokesCallerToken(t *testing.T) {
	token, err := generateJWT(&User{ID: 5, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)

	revocations := newFakeRevocations()
	w := httptest.NewRecorder()
	handleLogout(revocations)(w, logoutRequest(t, token))

	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Contains(t, revocations.revoked, jtiOf(t, token), "the caller's own jti must be revoked")
	assert.Equal(t, int64(5), revocations.lastUserID, "user_id comes from the string sub claim")
	assert.WithinDuration(t, time.Now().Add(tokenLifetime), revocations.lastExpiresAt, time.Minute,
		"expires_at must be the token's own exp")
}

// TestHandleLogout_Idempotent covers the handler and store contract: a repeat
// logout must not error. End to end the second call actually gets a 401,
// because requireAuth rejects the now-revoked token before the handler runs —
// the idempotency that matters is the store's ON CONFLICT DO NOTHING.
func TestHandleLogout_Idempotent(t *testing.T) {
	token, err := generateJWT(&User{ID: 5, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)

	revocations := newFakeRevocations()
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		handleLogout(revocations)(w, logoutRequest(t, token))
		assert.Equal(t, http.StatusNoContent, w.Code, "a repeat logout must not error")
	}
	assert.Equal(t, 2, revocations.revokeCalls, "both calls reach the store; the store deduplicates")
}

func TestHandleLogout_MissingClaims(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	w := httptest.NewRecorder()
	handleLogout(newFakeRevocations())(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "unauthorized", errorBody(t, w))
}

// TestHandleLogout_TokenWithoutJti covers the compatibility shim's logout
// side: there is nothing to revoke, but the client still gets a 204 so old
// and new clients see one contract.
// Not safe for t.Parallel(); uses global logger.
func TestHandleLogout_TokenWithoutJti(t *testing.T) {
	claims := jwt.MapClaims{
		"sub": "5",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iss": "vehicle-positions-api",
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req = req.WithContext(contextWithClaims(req.Context(), claims))

	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(original) })

	revocations := newFakeRevocations()
	w := httptest.NewRecorder()
	handleLogout(revocations)(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Zero(t, revocations.revokeCalls, "there is no jti to record")
	assert.Contains(t, logs.String(), "nothing to revoke")
}

// Not safe for t.Parallel(); uses global logger.
func TestHandleLogout_StoreError(t *testing.T) {
	token, err := generateJWT(&User{ID: 5, Email: "driver@test.com", Role: "driver"}, testSecret)
	require.NoError(t, err)

	revocations := newFakeRevocations()
	revocations.err = errors.New("database unavailable")

	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(original) })

	w := httptest.NewRecorder()
	handleLogout(revocations)(w, logoutRequest(t, token))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", errorBody(t, w))
	assert.Contains(t, logs.String(), "failed to revoke token",
		"a failed revocation must be logged, not swallowed")
}

// TestLoginLogoutRevokeFlow walks the whole lifecycle through the real mux:
// log in, use the token, log out, then find the same token rejected.
func TestLoginLogoutRevokeFlow(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password"), bcryptCost)
	require.NoError(t, err)
	users := &mockUserStore{user: &User{
		ID:           11,
		Email:        "driver@test.com",
		PasswordHash: string(hash),
		Role:         "driver",
		Active:       true,
	}}
	revocations := newFakeRevocations()

	login := handleLogin(users, testSecret, nil, false)
	w := postLogin(login, "driver@test.com", "password")
	require.Equal(t, http.StatusOK, w.Code)
	var loginResp LoginResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&loginResp))
	require.NotEmpty(t, loginResp.Token)

	authed := requireAuth(testSecret, revocations)
	protected := authed(dummyHandler())

	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/locations", nil)
		req.Header.Set("Authorization", "Bearer "+loginResp.Token)
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, call().Code, "the fresh token must work")

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	logoutRec := httptest.NewRecorder()
	authed(handleLogout(revocations)).ServeHTTP(logoutRec, logoutReq)
	require.Equal(t, http.StatusNoContent, logoutRec.Code)

	after := call()
	assert.Equal(t, http.StatusUnauthorized, after.Code, "the same token must now be rejected")
	assert.Equal(t, "invalid token", errorBody(t, after))
}
