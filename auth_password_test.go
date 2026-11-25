package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// fakePasswordChanger is an OwnPasswordChanger for one account whose password
// is "old password".
type fakePasswordChanger struct {
	hash      string
	getErr    error
	changeErr error
	changes   []passwordChange
	actors    []auditActor
}

func newFakePasswordChanger(t *testing.T) *fakePasswordChanger {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("old password"), bcrypt.MinCost)
	require.NoError(t, err)
	return &fakePasswordChanger{hash: string(hash)}
}

func (f *fakePasswordChanger) GetUserCredentials(context.Context, int64) (string, string, error) {
	return "ana@test.com", f.hash, f.getErr
}

func (f *fakePasswordChanger) ChangeOwnPassword(ctx context.Context, change passwordChange) error {
	f.changes = append(f.changes, change)
	actor, _ := ctx.Value(auditActorKey).(auditActor)
	f.actors = append(f.actors, actor)
	return f.changeErr
}

const changeBody = `{"current_password":"old password","new_password":"new password","refresh_token":"r1"}`

func changePassword(handler http.HandlerFunc, sub, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	claims := jwt.MapClaims{"sub": sub, "role": roleDriver, "email": "ana@test.com"}
	req = req.WithContext(context.WithValue(req.Context(), claimsKey, claims))
	req.RemoteAddr = "203.0.113.9:4100"
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandleChangePassword(t *testing.T) {
	store := newFakePasswordChanger(t)
	before := time.Now()
	w := changePassword(handleChangePassword(store, nil, false), "42", "application/json; charset=utf-8", changeBody)

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Empty(t, w.Body.String())
	require.Len(t, store.changes, 1)
	change := store.changes[0]
	assert.WithinRange(t, change.At, before, time.Now())
	change.At = time.Time{}
	assert.Equal(t, passwordChange{UserID: 42, CheckedHash: store.hash, NewPassword: "new password", KeepTokenHash: hashRefreshToken("r1")}, change)
	assert.Equal(t, auditActor{UserID: 42, Email: "ana@test.com", IP: "203.0.113.9"}, store.actors[0])
}

func TestHandleChangePassword_Refuses(t *testing.T) {
	body := func(current, next, token string) string {
		b, err := json.Marshal(ChangePasswordRequest{CurrentPassword: current, NewPassword: next, RefreshToken: token})
		require.NoError(t, err)
		return string(b)
	}
	const jsonType = "application/json"
	const required = "current_password, new_password and refresh_token are required"
	tests := []struct {
		name        string
		sub         string
		contentType string
		body        string
		getErr      error
		changeErr   error
		code        int
		want        string
	}{
		{"another content type", "42", "text/plain", changeBody, nil, nil, http.StatusUnsupportedMediaType, "Content-Type must be application/json"},
		{"a subject that is no number", "rider", jsonType, changeBody, nil, nil, http.StatusUnauthorized, "invalid token: invalid subject"},
		{"no JSON", "42", jsonType, "{", nil, nil, http.StatusBadRequest, "invalid JSON: unexpected EOF"},
		{"an unknown field", "42", jsonType, `{"old":"x"}`, nil, nil, http.StatusBadRequest, `invalid JSON: json: unknown field "old"`},
		{"something after it", "42", jsonType, changeBody + "{}", nil, nil, http.StatusBadRequest, "invalid JSON: request body must contain a single JSON object and no trailing data"},
		{"a body too large", "42", jsonType, body("old password", strings.Repeat("n", 1<<10), "r1"), nil, nil, http.StatusRequestEntityTooLarge, "request body too large"},
		{"no current password", "42", jsonType, body("", "new password", "r1"), nil, nil, http.StatusBadRequest, required},
		{"no new password", "42", jsonType, body("old password", "", "r1"), nil, nil, http.StatusBadRequest, required},
		{"no refresh token", "42", jsonType, body("old password", "new password", ""), nil, nil, http.StatusBadRequest, required},
		{"a new password too short", "42", jsonType, body("old password", "short", "r1"), nil, nil, http.StatusBadRequest, "new_password must be at least 8 characters"},
		{"a new password too long", "42", jsonType, body("old password", strings.Repeat("n", 73), "r1"), nil, nil, http.StatusBadRequest, "new_password must be at most 72 bytes"},
		// Not a 401, which the apps read as a sign-in that is over.
		{"a wrong current password", "42", jsonType, body("not it", "new password", "r1"), nil, nil, http.StatusUnprocessableEntity, "current password is wrong"},
		{"an account that is gone", "42", jsonType, changeBody, ErrUserNotFound, nil, http.StatusUnauthorized, "invalid token"},
		{"a lookup that fails", "42", jsonType, changeBody, errors.New("boom"), nil, http.StatusInternalServerError, "internal server error"},
		{"a password changed meanwhile", "42", jsonType, changeBody, nil, ErrPasswordChangedMeanwhile, http.StatusUnprocessableEntity, "current password is wrong"},
		{"a token that can't be kept", "42", jsonType, changeBody, nil, ErrRefreshTokenNotFound, http.StatusConflict, "refresh_token is not a current one of this account's"},
		{"an account deleted meanwhile", "42", jsonType, changeBody, nil, ErrUserNotFound, http.StatusUnauthorized, "invalid token"},
		{"a change that fails", "42", jsonType, changeBody, nil, errors.New("boom"), http.StatusInternalServerError, "internal server error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakePasswordChanger(t)
			store.getErr, store.changeErr = tt.getErr, tt.changeErr
			w := changePassword(handleChangePassword(store, nil, false), tt.sub, tt.contentType, tt.body)

			assert.Equal(t, tt.code, w.Code)
			assert.Equal(t, tt.want, decodeError(t, w))
			if tt.changeErr == nil {
				assert.Empty(t, store.changes, "nothing is changed")
			}
		})
	}
}

// TestHandleChangePassword_LongestPasswordsFit: two passwords at their limit fit
// in the body in the longest form JSON has for them.
func TestHandleChangePassword_LongestPasswordsFit(t *testing.T) {
	store := newFakePasswordChanger(t)
	longest := strings.Repeat(`\u0001`, maxPasswordBytes)
	body := `{"current_password":"` + longest + `","new_password":"` + longest + `","refresh_token":"` + strings.Repeat("r", 64) + `"}`
	w := changePassword(handleChangePassword(store, nil, false), "42", "application/json", body)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
}

// TestHandleChangePassword_SharesTheLoginBudget: wrong current passwords use up
// the account's login attempts, in either direction, and a right one gives them
// back.
func TestHandleChangePassword_SharesTheLoginBudget(t *testing.T) {
	limiter := NewLoginRateLimiter()
	t.Cleanup(limiter.Stop)
	store := newFakePasswordChanger(t)
	handler := handleChangePassword(store, limiter, false)
	wrong := strings.Replace(changeBody, "old password", "not it", 1)

	for range loginEmailLimit - 1 {
		require.Equal(t, http.StatusUnprocessableEntity, changePassword(handler, "42", "application/json", wrong).Code)
	}
	require.Equal(t, http.StatusNoContent, changePassword(handler, "42", "application/json", changeBody).Code)
	for range loginEmailLimit {
		require.Equal(t, http.StatusUnprocessableEntity, changePassword(handler, "42", "application/json", wrong).Code)
	}
	w := changePassword(handler, "42", "application/json", changeBody)
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "even the right one, until the minute is over")
	assert.Equal(t, "too many attempts", decodeError(t, w))
	assert.Len(t, store.changes, 1)
	assert.False(t, limiter.Allow("198.51.100.7", "ANA@test.com"), "the account's logins from anywhere wait too")
}

// signedIn is one phone signed in to a server.
type signedIn struct{ access, refresh string }

// TestChangePassword_ThroughTheServer: with two phones signed in to one
// account, one changes the password. The other is signed out at once, its
// access token and its refresh token both refused. The first goes on: its
// access token is ended with the rest, and its refresh token gets one that
// works in the same second. The new password signs in and the old one doesn't.
// Not safe for t.Parallel(); uses global logger.
func TestChangePassword_ThroughTheServer(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := context.Background()
	ana, err := store.CreateUser(ctx, "Ana", "aud-ana@example.org", "old password", roleDriver)
	require.NoError(t, err)
	bo, err := store.CreateUser(ctx, "Bo", "aud-bo@example.org", "bo password", roleDriver)
	require.NoError(t, err)
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	newLimiter := func() *LoginRateLimiter {
		l := NewLoginRateLimiter()
		t.Cleanup(l.Stop)
		return l
	}
	refreshLimiter := NewRefreshRateLimiter()
	t.Cleanup(refreshLimiter.Stop)
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return nil }, testReportSettings)
	h, err := newHandler(serverStore{store, &noopStore{}}, tracker, nil, newLimiter(), newLimiter(), refreshLimiter, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, trustProxy: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, nil, nil,
		newAlertWatcher(store, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)
	logs := captureLogs(t)
	send := func(method, path, access, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "198.51.100.40")
		if access != "" {
			req.Header.Set("Authorization", "Bearer "+access)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	// An access token a phone has had for a minute.
	older := func(user *UserResponse) string {
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": strconv.FormatInt(user.ID, 10), "email": user.Email, "role": roleDriver, "jti": uniqueJTI(t),
			"iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(), "iss": "vehicle-positions-api",
		}).SignedString(testSecret)
		require.NoError(t, err)
		return token
	}
	refreshToken := func(user *UserResponse) string {
		token, _ := newStoredRefreshToken(t, store, user.ID, time.Now().Add(time.Hour))
		return token
	}
	first, second := signedIn{older(ana), refreshToken(ana)}, signedIn{older(ana), refreshToken(ana)}
	boPhone := signedIn{older(bo), refreshToken(bo)}
	mark := lastAuditID(t, store)
	change := func(access, current, next, refresh string) *httptest.ResponseRecorder {
		body, err := json.Marshal(ChangePasswordRequest{CurrentPassword: current, NewPassword: next, RefreshToken: refresh})
		require.NoError(t, err)
		return send(http.MethodPost, "/api/v1/auth/password", access, string(body))
	}
	vehicles := func(access string) int { return send(http.MethodGet, "/api/v1/vehicles", access, "").Code }
	refreshed := func(token string) *httptest.ResponseRecorder {
		return send(http.MethodPost, "/api/v1/auth/refresh", "", `{"refresh_token":"`+token+`"}`)
	}
	login := func(password string) int {
		return send(http.MethodPost, "/api/v1/auth/login", "", `{"email":"aud-ana@example.org","password":"`+password+`"}`).Code
	}

	// Refused, none of these changes anything.
	assert.Equal(t, http.StatusUnprocessableEntity, change(first.access, "not it", "new password", first.refresh).Code)
	assert.Equal(t, http.StatusConflict, change(first.access, "old password", "new password", "no such token").Code)
	assert.Equal(t, http.StatusConflict, change(first.access, "old password", "new password", boPhone.refresh).Code, "another account's")
	assert.Equal(t, http.StatusUnauthorized, change("", "old password", "new password", first.refresh).Code)
	assert.Empty(t, auditRowsAfter(t, store, mark))
	require.Equal(t, http.StatusOK, vehicles(second.access))

	w := change(first.access, "old password", "new password", first.refresh)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	assert.Equal(t, http.StatusUnauthorized, vehicles(second.access), "the other phone's access token")
	assert.Equal(t, http.StatusUnauthorized, refreshed(second.refresh).Code, "the other phone's refresh token")
	assert.Equal(t, http.StatusUnauthorized, vehicles(first.access), "this phone's access token is ended with the rest")
	w = refreshed(first.refresh)
	require.Equal(t, http.StatusOK, w.Code, "and its refresh token replaces it: %s", w.Body.String())
	var renewed LoginResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&renewed))
	assert.Equal(t, http.StatusOK, vehicles(renewed.AccessToken), "in the same second")
	assert.Equal(t, http.StatusOK, vehicles(boPhone.access), "another account goes on")
	assert.Equal(t, http.StatusOK, refreshed(boPhone.refresh).Code)
	assert.Equal(t, http.StatusUnauthorized, login("old password"))
	assert.Equal(t, http.StatusOK, login("new password"))

	rows := auditRowsAfter(t, store, mark)
	require.Len(t, rows, 1)
	assert.Equal(t, storedAuditRow{
		ActorID: &ana.ID, ActorEmail: "aud-ana@example.org", ActorIP: "198.51.100.40",
		ObjectType: auditUser, ObjectID: strconv.FormatInt(ana.ID, 10), Action: auditPassword,
	}, rows[0])
	for _, secret := range []string{"old password", "new password", "not it", first.refresh} {
		assert.NotContains(t, logs.String(), secret)
	}
}

// TestStore_ChangeOwnPassword_Refuses: a change the account's current state
// does not allow leaves the password and every token as they were.
func TestStore_ChangeOwnPassword_Refuses(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)
	_, checked, err := store.GetUserCredentials(ctx, userID)
	require.NoError(t, err)
	_, live := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	_, expired := newStoredRefreshToken(t, store, userID, time.Now().Add(-time.Minute))
	_, spent := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	token, err := store.GetRefreshToken(ctx, spent)
	require.NoError(t, err)
	_, next := newRefreshHash(t)
	rotated, err := store.RotateRefreshToken(ctx, token.ID, next, userID, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.True(t, rotated)
	_, others := newStoredRefreshToken(t, store, insertRefreshTestUser(t, store), time.Now().Add(time.Hour))

	tests := []struct {
		name   string
		change passwordChange
		want   error
	}{
		{"an unknown account", passwordChange{UserID: -1, CheckedHash: checked, KeepTokenHash: live}, ErrUserNotFound},
		{"a password changed since it was checked", passwordChange{UserID: userID, CheckedHash: "another", KeepTokenHash: live}, ErrPasswordChangedMeanwhile},
		{"an unknown token", passwordChange{UserID: userID, CheckedHash: checked, KeepTokenHash: "nothing"}, ErrRefreshTokenNotFound},
		{"another account's token", passwordChange{UserID: userID, CheckedHash: checked, KeepTokenHash: others}, ErrRefreshTokenNotFound},
		{"an expired token", passwordChange{UserID: userID, CheckedHash: checked, KeepTokenHash: expired}, ErrRefreshTokenNotFound},
		{"a spent token", passwordChange{UserID: userID, CheckedHash: checked, KeepTokenHash: spent}, ErrRefreshTokenNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.change.NewPassword, tt.change.At = "new password", time.Now()
			assert.ErrorIs(t, store.ChangeOwnPassword(ctx, tt.change), tt.want)
			_, hash, err := store.GetUserCredentials(ctx, userID)
			require.NoError(t, err)
			assert.Equal(t, checked, hash)
			assert.Equal(t, 4, countUserRefreshRows(t, store, userID))
			assert.Equal(t, 1, countRefreshRows(t, store, live))
		})
	}
	_, _, err = store.GetUserCredentials(ctx, -1)
	assert.ErrorIs(t, err, ErrUserNotFound)
}

// TestStore_ChangeOwnPassword_KeepsALaterEnd: an end set for later than the
// change, as by a clock that ran ahead, is not brought forward, which would
// make tokens it ended good again.
func TestStore_ChangeOwnPassword_KeepsALaterEnd(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	userID := insertRefreshTestUser(t, store)
	_, checked, err := store.GetUserCredentials(ctx, userID)
	require.NoError(t, err)
	_, live := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	later := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	_, err = store.pool.Exec(ctx, `UPDATE users SET sessions_valid_after = $2 WHERE id = $1`, userID, later)
	require.NoError(t, err)

	require.NoError(t, store.ChangeOwnPassword(ctx, passwordChange{UserID: userID, CheckedHash: checked, NewPassword: "new password", KeepTokenHash: live, At: time.Now()}))
	var end time.Time
	require.NoError(t, store.pool.QueryRow(ctx, `SELECT sessions_valid_after FROM users WHERE id = $1`, userID).Scan(&end))
	assert.True(t, later.Equal(end), "%s, not %s", later, end)
}

// TestDriverPasswordRoute_Wiring: a viewer, as everywhere in the driver API, is
// refused, and so is a rider.
func TestDriverPasswordRoute_Wiring(t *testing.T) {
	mux := newMux(&noopStore{}, nil, nil, testSecret, testTTLs, time.Time{}, nil, nil, false, false, "", nil, nil, nil, newAlertWatcher(&noopStore{}, testAlertSettings), nil, newFeedHealth(time.Now))
	riderToken, err := generateRiderJWT("rider-1", testSecret, time.Hour)
	require.NoError(t, err)
	viewerToken, err := generateJWT(&User{ID: 3, Email: "v@test.com", Role: roleViewer}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)
	for _, token := range []string{riderToken, viewerToken} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(changeBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	}
}

// uniqueJTI is a token id no other test has.
func uniqueJTI(t *testing.T) string {
	t.Helper()
	value, err := newRefreshTokenValue()
	require.NoError(t, err)
	return value
}

// newRefreshHash is a refresh token that is not stored, and its hash.
func newRefreshHash(t *testing.T) (string, string) {
	t.Helper()
	token, err := newRefreshTokenValue()
	require.NoError(t, err)
	return token, hashRefreshToken(token)
}
