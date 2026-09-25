package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// testTTLs are the lifetimes handler tests run with. They are the production
// defaults so a test never proves something the shipped configuration would
// not.
var testTTLs = tokenTTLs{access: defaultAccessTokenTTL, refresh: defaultRefreshTokenTTL}

// fakeRefreshTokens is an in-memory stand-in for the refresh-token half of
// the store, keyed by hash exactly as the real table is. It also answers the
// user lookup, so one double satisfies RefreshTokenCreator (login),
// RefreshStore (refresh), and RefreshTokenDeleter (logout).
//
// The per-method error fields exist because the handlers must react
// differently to each failure: a create that fails is a failed login, a get
// that fails is a 500, and a rotation that fails must not hand out a token.
type fakeRefreshTokens struct {
	byHash map[string]*RefreshToken
	nextID int64

	user    *UserResponse
	userErr error

	createErr error
	getErr    error
	rotateErr error
	deleteErr error

	// rotateLoses makes rotation report that the row was consumed between the
	// handler's read and its update, which is what the store returns when a
	// concurrent refresh won the compare-and-set.
	rotateLoses bool

	createCalls  int
	deletedUsers []int64
}

func newFakeRefreshTokens() *fakeRefreshTokens {
	return &fakeRefreshTokens{byHash: make(map[string]*RefreshToken), nextID: 1}
}

func (f *fakeRefreshTokens) CreateRefreshToken(_ context.Context, tokenHash string, userID int64, expiresAt time.Time) error {
	if f.createErr != nil {
		return f.createErr
	}
	if _, exists := f.byHash[tokenHash]; exists {
		return errors.New("duplicate token hash")
	}
	f.createCalls++
	f.byHash[tokenHash] = &RefreshToken{
		ID:        f.nextID,
		UserID:    userID,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	f.nextID++
	return nil
}

func (f *fakeRefreshTokens) GetRefreshToken(_ context.Context, tokenHash string) (*RefreshToken, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	token, ok := f.byHash[tokenHash]
	if !ok {
		return nil, ErrRefreshTokenNotFound
	}
	copied := *token
	return &copied, nil
}

func (f *fakeRefreshTokens) RotateRefreshToken(_ context.Context, usedID int64, newHash string, userID int64, expiresAt time.Time) (bool, error) {
	if f.rotateErr != nil {
		return false, f.rotateErr
	}
	if f.rotateLoses {
		return false, nil
	}
	for _, token := range f.byHash {
		if token.ID != usedID {
			continue
		}
		if token.UsedAt != nil {
			return false, nil
		}
		now := time.Now()
		token.UsedAt = &now
		f.byHash[newHash] = &RefreshToken{
			ID:        f.nextID,
			UserID:    userID,
			ExpiresAt: expiresAt,
			CreatedAt: now,
		}
		f.nextID++
		return true, nil
	}
	return false, nil
}

func (f *fakeRefreshTokens) DeleteRefreshTokensForUser(_ context.Context, userID int64) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedUsers = append(f.deletedUsers, userID)
	for hash, token := range f.byHash {
		if token.UserID == userID {
			delete(f.byHash, hash)
		}
	}
	return nil
}

func (f *fakeRefreshTokens) GetUser(_ context.Context, id int64) (*UserResponse, error) {
	if f.userErr != nil {
		return nil, f.userErr
	}
	if f.user == nil || f.user.ID != id {
		return nil, ErrUserNotFound
	}
	return f.user, nil
}

var _ RefreshTokenCreator = (*fakeRefreshTokens)(nil)
var _ RefreshTokenDeleter = (*fakeRefreshTokens)(nil)
var _ RefreshStore = (*fakeRefreshTokens)(nil)

// storeRefreshToken seeds a token for userID and returns the plaintext the
// client would hold.
func storeRefreshToken(t *testing.T, f *fakeRefreshTokens, userID int64, expiresAt time.Time) string {
	t.Helper()
	token, err := newRefreshTokenValue()
	require.NoError(t, err)
	require.NoError(t, f.CreateRefreshToken(context.Background(), hashRefreshToken(token), userID, expiresAt))
	return token
}

// refreshStoreWithUser returns a fake holding one active driver plus a valid
// refresh token for them.
func refreshStoreWithUser(t *testing.T) (*fakeRefreshTokens, string) {
	t.Helper()
	f := newFakeRefreshTokens()
	f.user = &UserResponse{ID: 42, Email: "driver@test.com", Role: "driver", Active: true}
	return f, storeRefreshToken(t, f, 42, time.Now().Add(defaultRefreshTokenTTL))
}

// postRefresh sends a well-formed refresh request carrying token.
func postRefresh(handler http.HandlerFunc, token string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(RefreshRequest{RefreshToken: token})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

// decodeTokens decodes a successful login/refresh body.
func decodeTokens(t *testing.T, w *httptest.ResponseRecorder) LoginResponse {
	t.Helper()
	var resp LoginResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp
}

func TestHandleRefresh_Success(t *testing.T) {
	f, token := refreshStoreWithUser(t)

	w := postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), token)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeTokens(t, w)
	require.NotEmpty(t, resp.AccessToken)
	assert.Equal(t, int(defaultAccessTokenTTL.Seconds()), resp.ExpiresIn)

	claims, err := parseSessionToken(resp.AccessToken, testSecret)
	require.NoError(t, err)
	assert.Equal(t, "42", claims["sub"], "the new access token must belong to the refresh token's user")
	assert.Equal(t, "driver@test.com", claims["email"])
	assert.Equal(t, "driver", claims["role"])
	assert.NotEmpty(t, claims["jti"], "a refreshed token must be revocable too")
}

// TestHandleRefresh_ReturnsBothTokenAndAccessToken pins the deprecated alias
// on the refresh path as well, so a client can parse both responses with one
// model.
func TestHandleRefresh_ReturnsBothTokenAndAccessToken(t *testing.T) {
	f, token := refreshStoreWithUser(t)

	w := postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), token)

	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeTokens(t, w)
	require.NotEmpty(t, resp.Token)
	assert.Equal(t, resp.AccessToken, resp.Token)
}

func TestHandleRefresh_RotatesToken(t *testing.T) {
	f, token := refreshStoreWithUser(t)
	handler := handleRefreshToken(f, testSecret, testTTLs, nil, false)

	w := postRefresh(handler, token)
	require.Equal(t, http.StatusOK, w.Code)
	resp := decodeTokens(t, w)

	require.NotEmpty(t, resp.RefreshToken)
	assert.NotEqual(t, token, resp.RefreshToken, "rotation must hand back a different token")

	old, err := f.GetRefreshToken(context.Background(), hashRefreshToken(token))
	require.NoError(t, err)
	require.NotNil(t, old.UsedAt, "the presented token must be marked used")
	assert.WithinDuration(t, time.Now(), *old.UsedAt, time.Minute)

	fresh, err := f.GetRefreshToken(context.Background(), hashRefreshToken(resp.RefreshToken))
	require.NoError(t, err)
	assert.Nil(t, fresh.UsedAt, "the replacement must be unused")
	assert.Equal(t, int64(42), fresh.UserID)
	assert.WithinDuration(t, time.Now().Add(defaultRefreshTokenTTL), fresh.ExpiresAt, time.Minute)

	// The replacement must itself work, or rotation has broken the chain.
	second := postRefresh(handler, resp.RefreshToken)
	assert.Equal(t, http.StatusOK, second.Code)
}

func TestHandleRefresh_RejectsReusedToken(t *testing.T) {
	f, token := refreshStoreWithUser(t)
	handler := handleRefreshToken(f, testSecret, testTTLs, nil, false)

	require.Equal(t, http.StatusOK, postRefresh(handler, token).Code)

	w := postRefresh(handler, token)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, invalidRefreshTokenMessage, errorBody(t, w))
}

func TestHandleRefresh_RejectsExpiredToken(t *testing.T) {
	f := newFakeRefreshTokens()
	f.user = &UserResponse{ID: 42, Email: "driver@test.com", Role: "driver", Active: true}
	token := storeRefreshToken(t, f, 42, time.Now().Add(-time.Second))

	w := postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), token)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, invalidRefreshTokenMessage, errorBody(t, w))
}

func TestHandleRefresh_RejectsUnknownToken(t *testing.T) {
	f, _ := refreshStoreWithUser(t)

	unknown, err := newRefreshTokenValue()
	require.NoError(t, err)

	w := postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), unknown)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, invalidRefreshTokenMessage, errorBody(t, w))
}

// TestHandleRefresh_RejectionsAreIndistinguishable is the guard on the
// property the individual rejection tests only assert one at a time: an
// attacker holding a token must not be able to tell an unknown token from an
// expired or spent one.
func TestHandleRefresh_RejectionsAreIndistinguishable(t *testing.T) {
	handler := func(f *fakeRefreshTokens) http.HandlerFunc {
		return handleRefreshToken(f, testSecret, testTTLs, nil, false)
	}

	spentStore, spent := refreshStoreWithUser(t)
	require.Equal(t, http.StatusOK, postRefresh(handler(spentStore), spent).Code)

	expiredStore := newFakeRefreshTokens()
	expiredStore.user = &UserResponse{ID: 42, Email: "driver@test.com", Role: "driver", Active: true}
	expired := storeRefreshToken(t, expiredStore, 42, time.Now().Add(-time.Second))

	unknownStore, _ := refreshStoreWithUser(t)
	unknown, err := newRefreshTokenValue()
	require.NoError(t, err)

	deactivatedStore := newFakeRefreshTokens()
	deactivatedStore.user = &UserResponse{ID: 42, Email: "driver@test.com", Role: "driver", Active: false}
	deactivated := storeRefreshToken(t, deactivatedStore, 42, time.Now().Add(defaultRefreshTokenTTL))

	tests := []struct {
		name  string
		store *fakeRefreshTokens
		token string
	}{
		{"already used", spentStore, spent},
		{"expired", expiredStore, expired},
		{"unknown", unknownStore, unknown},
		{"deactivated user", deactivatedStore, deactivated},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := postRefresh(handler(tc.store), tc.token)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Equal(t, invalidRefreshTokenMessage, errorBody(t, w),
				"every rejection must return an identical body")
		})
	}
}

func TestHandleRefresh_RejectsDeactivatedUser(t *testing.T) {
	f := newFakeRefreshTokens()
	f.user = &UserResponse{ID: 42, Email: "driver@test.com", Role: "driver", Active: false}
	token := storeRefreshToken(t, f, 42, time.Now().Add(defaultRefreshTokenTTL))

	w := postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), token)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, invalidRefreshTokenMessage, errorBody(t, w))

	stored, err := f.GetRefreshToken(context.Background(), hashRefreshToken(token))
	require.NoError(t, err)
	assert.Nil(t, stored.UsedAt, "a rejected refresh must not consume the token")
}

// TestHandleRefresh_UserLookupError covers review item 5 on #62: a database
// failure looking up the token's owner is a 500, not a 401, and must not
// consume the token.
func TestHandleRefresh_UserLookupError(t *testing.T) {
	f, token := refreshStoreWithUser(t)
	f.userErr = errors.New("database unavailable")

	w := postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), token)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", errorBody(t, w))

	f.userErr = nil
	stored, err := f.GetRefreshToken(context.Background(), hashRefreshToken(token))
	require.NoError(t, err)
	assert.Nil(t, stored.UsedAt, "a transient failure must leave the token usable")
}

func TestHandleRefresh_TokenLookupError(t *testing.T) {
	f, token := refreshStoreWithUser(t)
	f.getErr = errors.New("database unavailable")

	w := postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), token)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", errorBody(t, w))
}

func TestHandleRefresh_RotationError(t *testing.T) {
	f, token := refreshStoreWithUser(t)
	f.rotateErr = errors.New("database unavailable")

	w := postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), token)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "internal server error", errorBody(t, w))
}

// TestHandleRefresh_LosesRotationRace covers the compare-and-set path: the
// row was consumed between the read and the update, so no token is issued.
func TestHandleRefresh_LosesRotationRace(t *testing.T) {
	f, token := refreshStoreWithUser(t)
	f.rotateLoses = true

	w := postRefresh(handleRefreshToken(f, testSecret, testTTLs, nil, false), token)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, invalidRefreshTokenMessage, errorBody(t, w))
}

func TestHandleRefresh_RateLimited(t *testing.T) {
	f, token := refreshStoreWithUser(t)
	limiter := NewLoginRateLimiter()
	defer limiter.Stop()

	handler := handleRefreshToken(f, testSecret, testTTLs, limiter, false)

	// Each refresh rotates, so present the token the previous call returned
	// and stay on the success path until the limiter itself trips.
	current := token
	for range loginIPLimit {
		w := postRefresh(handler, current)
		require.Equal(t, http.StatusOK, w.Code)
		current = decodeTokens(t, w).RefreshToken
	}

	w := postRefresh(handler, current)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "too many attempts", errorBody(t, w))
}

// TestHandleRefresh_RateLimitedBeforeStore verifies the limiter runs before
// the store is touched, so a flood cannot be turned into database load.
func TestHandleRefresh_RateLimitedBeforeStore(t *testing.T) {
	f, token := refreshStoreWithUser(t)
	f.getErr = errors.New("the store must not be reached")
	limiter := NewLoginRateLimiter()
	defer limiter.Stop()

	handler := handleRefreshToken(f, testSecret, testTTLs, limiter, false)
	for range loginIPLimit {
		require.Equal(t, http.StatusInternalServerError, postRefresh(handler, token).Code)
	}

	w := postRefresh(handler, token)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestHandleRefresh_MalformedBody(t *testing.T) {
	f, _ := refreshStoreWithUser(t)
	handler := handleRefreshToken(f, testSecret, testTTLs, nil, false)

	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantError   string
	}{
		{"empty body", "application/json", "", http.StatusBadRequest, "invalid JSON"},
		{"malformed JSON", "application/json", "{bad", http.StatusBadRequest, "invalid JSON"},
		{"unknown field", "application/json", `{"refresh_token":"x","extra":1}`, http.StatusBadRequest, "unknown field"},
		{"trailing data", "application/json", `{"refresh_token":"x"}{"more":1}`, http.StatusBadRequest, "trailing data"},
		{"missing token", "application/json", `{}`, http.StatusBadRequest, "refresh_token is required"},
		{"empty token", "application/json", `{"refresh_token":""}`, http.StatusBadRequest, "refresh_token is required"},
		{"wrong content type", "text/plain", `{"refresh_token":"x"}`, http.StatusUnsupportedMediaType, "Content-Type must be application/json"},
		{"charset parameter accepted", "application/json; charset=utf-8", `{"refresh_token":"nosuchtoken"}`, http.StatusUnauthorized, invalidRefreshTokenMessage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			handler(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)
			assert.Contains(t, errorBody(t, w), tc.wantError)
		})
	}
}

// TestHandleRefresh_OversizedBody pins the MaxBytesReader cap on this
// unauthenticated endpoint.
func TestHandleRefresh_OversizedBody(t *testing.T) {
	f, _ := refreshStoreWithUser(t)

	huge := `{"refresh_token":"` + string(bytes.Repeat([]byte("a"), 2<<10)) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader([]byte(huge)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleRefreshToken(f, testSecret, testTTLs, nil, false)(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, errorBody(t, w), "too large")
}

// TestAccessTokenTTL_Boundary pins the configured access lifetime end to end:
// a token is accepted just inside its TTL and rejected just outside it.
func TestAccessTokenTTL_Boundary(t *testing.T) {
	user := &User{ID: 1, Email: "driver@test.com", Role: "driver", Active: true}

	tests := []struct {
		name       string
		ttl        time.Duration
		wantStatus int
	}{
		{"just inside the TTL", time.Minute, http.StatusOK},
		{"just outside the TTL", -time.Second, http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token, err := generateJWT(user, testSecret, tc.ttl)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/locations", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			requireAuth(testSecret, newFakeRevocations())(dummyHandler()).ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}
}

// TestGenerateJWT_UsesGivenTTL pins that the lifetime comes from the argument
// rather than a constant inside generateJWT. The two defaults happen to agree
// today — the access TTL stays at 24h until the Android client can refresh —
// so the test uses TTLs that differ, which is the property that has to hold
// when ACCESS_TOKEN_TTL is lowered.
func TestGenerateJWT_UsesGivenTTL(t *testing.T) {
	user := &User{ID: 1, Email: "driver@test.com", Role: "driver"}

	short, err := generateJWT(user, testSecret, 15*time.Minute)
	require.NoError(t, err)
	long, err := generateJWT(user, testSecret, sessionLifetime)
	require.NoError(t, err)

	shortExp := expiryOf(t, short)
	longExp := expiryOf(t, long)

	assert.WithinDuration(t, time.Now().Add(15*time.Minute), shortExp, time.Minute)
	assert.WithinDuration(t, time.Now().Add(sessionLifetime), longExp, time.Minute)
	assert.True(t, longExp.After(shortExp), "a longer TTL must produce a later expiry")
}

// expiryOf returns a signed token's exp claim.
func expiryOf(t *testing.T, tokenStr string) time.Time {
	t.Helper()
	claims, err := parseSessionToken(tokenStr, testSecret)
	require.NoError(t, err)
	exp, err := claims.GetExpirationTime()
	require.NoError(t, err)
	require.NotNil(t, exp)
	return exp.Time
}

func TestValidateTokenTTLs(t *testing.T) {
	tests := []struct {
		name    string
		ttls    tokenTTLs
		wantErr string
	}{
		{"defaults", tokenTTLs{access: defaultAccessTokenTTL, refresh: defaultRefreshTokenTTL}, ""},
		{"zero access TTL", tokenTTLs{access: 0, refresh: time.Hour}, "ACCESS_TOKEN_TTL must be positive"},
		{"negative access TTL", tokenTTLs{access: -time.Minute, refresh: time.Hour}, "ACCESS_TOKEN_TTL must be positive"},
		{"refresh shorter than access", tokenTTLs{access: time.Hour, refresh: time.Minute}, "must be longer than"},
		{"refresh equal to access", tokenTTLs{access: time.Hour, refresh: time.Hour}, "must be longer than"},
		{"refresh one nanosecond longer", tokenTTLs{access: time.Hour, refresh: time.Hour + 1}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTokenTTLs(tc.ttls)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestLoginRefreshLogoutFlow walks the whole credential lifecycle through the
// real handlers: log in, renew, log out, and find the renewed refresh token
// refused. The last step is the one that matters — a logout the client can
// undo by refreshing is not a logout.
func TestLoginRefreshLogoutFlow(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password"), bcryptCost)
	require.NoError(t, err)
	users := &mockUserStore{user: &User{
		ID:           11,
		Email:        "driver@test.com",
		PasswordHash: string(hash),
		Role:         "driver",
		Active:       true,
	}}
	refreshTokens := newFakeRefreshTokens()
	refreshTokens.user = &UserResponse{ID: 11, Email: "driver@test.com", Role: "driver", Active: true}
	revocations := newFakeRevocations()

	login := handleLogin(users, refreshTokens, testSecret, testTTLs, nil, false)
	w := postLogin(login, "driver@test.com", "password")
	require.Equal(t, http.StatusOK, w.Code)
	issued := decodeTokens(t, w)
	require.NotEmpty(t, issued.RefreshToken)

	refresh := handleRefreshToken(refreshTokens, testSecret, testTTLs, nil, false)
	w = postRefresh(refresh, issued.RefreshToken)
	require.Equal(t, http.StatusOK, w.Code)
	renewed := decodeTokens(t, w)
	require.NotEmpty(t, renewed.RefreshToken)

	authed := requireAuth(testSecret, revocations)
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+renewed.AccessToken)
	logoutRec := httptest.NewRecorder()
	authed(handleLogout(revocations, refreshTokens)).ServeHTTP(logoutRec, logoutReq)
	require.Equal(t, http.StatusNoContent, logoutRec.Code)

	after := postRefresh(refresh, renewed.RefreshToken)
	assert.Equal(t, http.StatusUnauthorized, after.Code,
		"logging out must invalidate the refresh token, not just the access token")
	assert.Equal(t, invalidRefreshTokenMessage, errorBody(t, after))
}
