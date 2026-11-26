package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// signInStores are the two ways a sign-in reaches the store. Both store what
// they sign in with hash.
var signInStores = []struct {
	name string
	// keeps is whether the sign-in has a refresh token to keep.
	keeps  bool
	signIn func(ctx context.Context, s *Store, user *User, hash string) (time.Time, error)
}{
	{"API", true, func(ctx context.Context, s *Store, user *User, hash string) (time.Time, error) {
		return s.CreateRefreshToken(signingThrough(ctx, auditViaAPI), hash, user, time.Now().Add(time.Hour))
	}},
	{"admin", false, func(ctx context.Context, s *Store, user *User, _ string) (time.Time, error) {
		return s.RecordSignIn(signingThrough(ctx, auditViaAdmin), user)
	}},
}

// ownPasswordChange is the account with userID changing its password, keeping a
// sign-in of its own.
func ownPasswordChange(t *testing.T, store *Store, userID int64) func() error {
	t.Helper()
	_, kept := newStoredRefreshToken(t, store, userID, time.Now().Add(time.Hour))
	checked := checkedUser(t, store, userID).PasswordHash
	return func() error {
		return store.ChangeOwnPassword(context.Background(), passwordChange{
			UserID: userID, CheckedHash: checked, NewPassword: "new password", KeepTokenHash: kept,
		})
	}
}

// TestStore_SignIn_AccountChangedMeanwhile: a sign-in checked against an account
// that has since ended its sign-ins is refused and leaves nothing behind. Any
// other change to the account leaves it be.
func TestStore_SignIn_AccountChangedMeanwhile(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := context.Background()
	tests := []struct {
		name    string
		change  func(t *testing.T, userID int64) error
		refused bool
	}{
		{"its own password change", func(t *testing.T, userID int64) error { return ownPasswordChange(t, store, userID)() }, true},
		{"a reset", func(_ *testing.T, userID int64) error {
			return store.SetUserPasswordAndRevokeSessions(ctx, userID, "new password")
		}, true},
		{"deactivated", func(_ *testing.T, userID int64) error {
			return store.SetUserActiveAndRevokeSessions(ctx, userID, false)
		}, true},
		{"deleted", func(_ *testing.T, userID int64) error {
			_, err := store.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)
			return err
		}, true},
		{"renamed", func(_ *testing.T, userID int64) error {
			_, err := store.UpdateUser(ctx, userID, "Ana Diaz", "aud-ana@example.org", roleDriver)
			return err
		}, false},
		{"nothing", func(*testing.T, int64) error { return nil }, false},
	}
	for _, way := range signInStores {
		for _, tt := range tests {
			t.Run(way.name+": "+tt.name, func(t *testing.T) {
				fx := newAuditFixture(t, store)
				user := checkedUser(t, store, fx.userID)
				require.NoError(t, tt.change(t, fx.userID))
				mark := lastAuditID(t, store)
				before := time.Now()

				at, err := way.signIn(asTestAdmin(), store, user, "aud-hash")

				if tt.refused {
					require.ErrorIs(t, err, ErrAccountChangedMeanwhile)
					assert.Zero(t, at)
					assert.Zero(t, countRefreshRows(t, store, "aud-hash"))
					assert.Empty(t, auditRowsAfter(t, store, mark))
					return
				}
				require.NoError(t, err)
				assert.WithinRange(t, at, before, time.Now())
				rows := auditRowsAfter(t, store, mark)
				require.Len(t, rows, 1)
				assert.Equal(t, auditSignIn, rows[0].Action)
				if way.keeps {
					assert.Equal(t, 1, countRefreshRows(t, store, "aud-hash"))
				}
			})
		}
	}
}

// holdUser parks every sign-in and every change that ends sign-ins of the
// account with userID, as holdVehicle parks starts. commit lets them on with
// what the holder wrote.
func holdUser(t *testing.T, userID int64, stmt string) (pid int, commit func()) {
	t.Helper()
	ctx := context.Background()
	holder, err := dedicatedConn(t).Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holder.Rollback(context.Background()) })
	_, err = holder.Exec(ctx, stmt, userID)
	require.NoError(t, err)
	require.NoError(t, holder.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid))
	return pid, func() { require.NoError(t, holder.Commit(ctx)) }
}

// TestStore_SignIn_WaitsForAChange: a sign-in that comes while a change to the
// account is under way waits for it, and then goes by what it left. Its token
// counts as issued after it.
func TestStore_SignIn_WaitsForAChange(t *testing.T) {
	store := newAuditTestStore(t)
	type signedIn struct {
		at  time.Time
		err error
	}
	tests := []struct {
		name, stmt string
		refused    bool
	}{
		{"a new password", "UPDATE users SET password_hash = 'another' WHERE id = $1", true},
		{"deactivation", "UPDATE users SET active = false WHERE id = $1", true},
		{"a change that fails", "SELECT 1 FROM users WHERE id = $1 FOR UPDATE", false},
	}
	for _, way := range signInStores {
		for _, tt := range tests {
			t.Run(way.name+": "+tt.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(asTestAdmin(), 30*time.Second)
				defer cancel()
				fx := newAuditFixture(t, store)
				user := checkedUser(t, store, fx.userID)
				pid, commit := holdUser(t, fx.userID, tt.stmt)

				done := make(chan signedIn, 1)
				go func() {
					at, err := way.signIn(ctx, store, user, "aud-hash")
					done <- signedIn{at, err}
				}()
				require.Eventually(t, func() bool { return len(done) == 1 || blockedBehind(store, pid, 1) == 1 },
					5*time.Second, 10*time.Millisecond, "the sign-in neither finished nor waited")
				require.Empty(t, done, "the sign-in did not wait")
				released := time.Now()
				commit()

				got := <-done
				if tt.refused {
					require.ErrorIs(t, got.err, ErrAccountChangedMeanwhile)
					assert.Zero(t, countRefreshRows(t, store, "aud-hash"))
					return
				}
				require.NoError(t, got.err)
				assert.False(t, got.at.Before(released), "signed in at %s, before the lock was had at %s", got.at, released)
			})
		}
	}
}

// TestStore_ChangeEndsASignInItWaitedFor: a change that ends an account's
// sign-ins and waited for one to be done ends that one too: its token is refused
// for good, when the account is active in its role again, and its refresh token
// is gone. So too when this server's clock and the database's disagree.
func TestStore_ChangeEndsASignInItWaitedFor(t *testing.T) {
	store := newAuditTestStore(t)
	ctx, cancel := context.WithTimeout(asTestAdmin(), 60*time.Second)
	defer cancel()
	tests := []struct {
		name   string
		change func(t *testing.T, userID int64) func() error
		undo   func(userID int64) error
		// endsToken and endsRefresh are what the change ends: a reset lets access
		// tokens run out, and a role change leaves a sign-in to renew in the role.
		endsToken, endsRefresh bool
	}{
		{"its own password change", func(t *testing.T, userID int64) func() error { return ownPasswordChange(t, store, userID) },
			func(int64) error { return nil }, true, true},
		{"a reset", func(_ *testing.T, userID int64) func() error {
			return func() error { return store.SetUserPasswordAndRevokeSessions(ctx, userID, "new password") }
		}, func(int64) error { return nil }, false, true},
		{"deactivation", func(_ *testing.T, userID int64) func() error {
			return func() error { return store.SetUserActiveAndRevokeSessions(ctx, userID, false) }
		}, func(userID int64) error { return store.SetUserActiveAndRevokeSessions(ctx, userID, true) }, true, true},
		{"a role change", func(_ *testing.T, userID int64) func() error {
			return func() error {
				_, err := store.UpdateUser(ctx, userID, "Ana", "aud-ana@example.org", roleAdmin)
				return err
			}
		}, func(userID int64) error {
			_, err := store.UpdateUser(ctx, userID, "Ana", "aud-ana@example.org", roleDriver)
			return err
		}, true, false},
	}
	skews := []struct {
		name string
		by   time.Duration
	}{
		{"", 0},
		{"this server's clock ahead, ", 2 * time.Second},
		{"this server's clock behind, ", -2 * time.Second},
	}
	for _, skew := range skews {
		for _, way := range signInStores {
			for _, tt := range tests {
				t.Run(skew.name+way.name+": "+tt.name, func(t *testing.T) {
					store.clock = func() time.Time { return time.Now().Add(skew.by) }
					t.Cleanup(func() { store.clock = nil })
					fx := newAuditFixture(t, store)
					user := checkedUser(t, store, fx.userID)
					change := tt.change(t, fx.userID)
					pid, commit := holdUser(t, fx.userID, "SELECT 1 FROM users WHERE id = $1 FOR UPDATE")

					type result struct {
						at  time.Time
						err error
					}
					signedIn, changed := make(chan result, 1), make(chan error, 1)
					go func() {
						at, err := way.signIn(ctx, store, user, "aud-hash")
						signedIn <- result{at, err}
					}()
					require.Eventually(t, func() bool { return blockedBehind(store, pid, 1) == 1 },
						5*time.Second, 10*time.Millisecond, "the sign-in never waited")
					go func() { changed <- change() }()
					require.Eventually(t, func() bool { return blockedBehind(store, pid, 1)+blockedBehind(store, pid, 2) >= 2 },
						5*time.Second, 10*time.Millisecond, "the change never queued behind the sign-in")
					commit()

					got := <-signedIn
					require.NoError(t, got.err)
					require.NoError(t, <-changed)
					require.NoError(t, tt.undo(fx.userID))

					good, err := store.IsUserActiveInRole(ctx, fx.userID, roleDriver, got.at)
					require.NoError(t, err)
					assert.Equal(t, !tt.endsToken, good, "its token")
					if way.keeps {
						want := 1
						if tt.endsRefresh {
							want = 0
						}
						assert.Equal(t, want, countRefreshRows(t, store, "aud-hash"), "its refresh token")
					}
				})
			}
		}
	}
}

// changingStore is the server's store, with a change that comes after a sign-in
// has read the account and before it is done.
type changingStore struct {
	serverStore
	meanwhile func() error
}

func (s *changingStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	user, err := s.serverStore.GetUserByEmail(ctx, email)
	if err == nil && s.meanwhile != nil {
		err = s.meanwhile()
	}
	return user, err
}

// notedSignIns is a FailedSignInNoter that keeps what it was told.
type notedSignIns struct{ noted []failedSignIn }

func (n *notedSignIns) NoteFailedSignIn(user *User, ip, via string) {
	n.noted = append(n.noted, failedSignIn{userID: user.ID, ip: ip, via: via})
}

// TestSignIn_RacingAPasswordChange_ThroughTheServer: a sign-in with the password
// that another phone changes while it is under way is answered as one with a
// wrong password, hands out nothing, and is noted as failed. The next one, with
// the new password, signs in, and its token is good at once.
func TestSignIn_RacingAPasswordChange_ThroughTheServer(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := context.Background()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return nil }, testReportSettings)

	ways := []struct {
		name, via string
		send      func(h http.Handler, password string) *httptest.ResponseRecorder
		// credential is what the answer signs in with, "" for nothing.
		credential func(t *testing.T, w *httptest.ResponseRecorder) string
		ok         int
		refusal    string
		// use sends it back, and returns the answer's code.
		use func(h http.Handler, credential string) int
	}{
		{"API", auditViaAPI, func(h http.Handler, password string) *httptest.ResponseRecorder {
			return postLogin(h.ServeHTTP, "aud-ana@example.org", password)
		}, func(t *testing.T, w *httptest.ResponseRecorder) string {
			var tokens LoginResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tokens))
			return tokens.AccessToken
		}, http.StatusOK, "invalid email or password", func(h http.Handler, credential string) int {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
			req.Header.Set("Authorization", "Bearer "+credential)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			return w.Code
		}},
		{"admin", auditViaAdmin, func(h http.Handler, password string) *httptest.ResponseRecorder {
			form := url.Values{"email": {"aud-ana@example.org"}, "password": {password}}
			req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			return w
		}, func(_ *testing.T, w *httptest.ResponseRecorder) string {
			for _, c := range w.Result().Cookies() {
				if c.Name == sessionCookieName {
					return c.Value
				}
			}
			return ""
		}, http.StatusSeeOther, "Invalid email or password.", func(h http.Handler, credential string) int {
			req := httptest.NewRequest(http.MethodGet, "/admin/more", nil)
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: credential})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			return w.Code
		}},
	}
	for _, way := range ways {
		t.Run(way.name, func(t *testing.T) {
			clearAuditFixture(t, store)
			ana, err := store.CreateUser(ctx, "Ana", "aud-ana@example.org", "old password", roleAdmin)
			require.NoError(t, err)
			noter := &notedSignIns{}
			racing := &changingStore{serverStore: serverStore{store, noter}}
			limiter := NewLoginRateLimiter()
			t.Cleanup(limiter.Stop)
			h, err := newHandler(racing, tracker, nil, limiter, limiter, nil, testSecret, testTTLs, time.Now(),
				adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, nil, nil,
				newAlertWatcher(store, testAlertSettings), reports, newFeedHealth(time.Now))
			require.NoError(t, err)
			racing.meanwhile = ownPasswordChange(t, store, ana.ID)
			mark := lastAuditID(t, store)

			w := way.send(h, "old password")

			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Contains(t, w.Body.String(), way.refusal)
			assert.Empty(t, way.credential(t, w))
			assert.Equal(t, []failedSignIn{{userID: ana.ID, ip: "192.0.2.1", via: way.via}}, noter.noted)
			rows := auditRowsAfter(t, store, mark)
			require.Len(t, rows, 1)
			assert.Equal(t, auditPassword, rows[0].Action, "the change, and no sign-in")
			var kept, seen int
			require.NoError(t, store.pool.QueryRow(ctx, "SELECT count(*) FROM refresh_tokens WHERE user_id = $1", ana.ID).Scan(&kept))
			assert.Equal(t, 1, kept, "only the other phone's")
			require.NoError(t, store.pool.QueryRow(ctx, "SELECT count(last_seen_at) FROM users WHERE id = $1", ana.ID).Scan(&seen))
			assert.Zero(t, seen, "not a sign-in")

			racing.meanwhile = nil
			require.Equal(t, http.StatusUnauthorized, way.send(h, "old password").Code)
			w = way.send(h, "new password")
			require.Equal(t, way.ok, w.Code, w.Body.String())
			credential := way.credential(t, w)
			require.NotEmpty(t, credential)
			assert.Equal(t, http.StatusOK, way.use(h, credential))
		})
	}
}

// TestSignIn_TokenCountsAsIssuedAtTheSignIn: both ways in pass the store the
// account as they checked it, and the token counts as issued when the store says
// the sign-in was done, not when the token was made.
func TestSignIn_TokenCountsAsIssuedAtTheSignIn(t *testing.T) {
	at := time.Now().Add(-3 * time.Minute).Truncate(time.Microsecond)
	issuedAt := func(t *testing.T, token string) time.Time {
		claims, err := parseSessionToken(token, testSecret)
		require.NoError(t, err)
		issued, ok := tokenIssuedAt(claims)
		require.True(t, ok)
		return issued
	}

	t.Run("API", func(t *testing.T) {
		user, tokens := sessionAccount(t, roleAdmin), newFakeRefreshTokens()
		tokens.signedInAt = at
		w := postLogin(handleLogin(&mockUserStore{user: user}, tokens, &noopStore{}, testSecret, testTTLs, nil, false), user.Email, sessionPassword)
		require.Equal(t, http.StatusOK, w.Code)
		issued := issuedAt(t, decodeTokens(t, w).AccessToken)
		assert.True(t, at.Equal(issued), "%s, not %s", at, issued)
		assert.Equal(t, []*User{user}, tokens.checked)
	})

	t.Run("admin", func(t *testing.T) {
		ui, user := newTestAdminUI(t), sessionAccount(t, roleAdmin)
		ui.users = &fakeUserFetcher{users: map[string]*User{user.Email: user}}
		signIns := &fakeSignIns{at: at}
		ui.signIns = signIns
		w := postAdminLogin(t, ui, user.Email)
		require.Equal(t, http.StatusSeeOther, w.Code)
		cookies := w.Result().Cookies()
		require.Len(t, cookies, 1)
		issued := issuedAt(t, cookies[0].Value)
		assert.True(t, at.Equal(issued), "%s, not %s", at, issued)
		assert.Equal(t, []*User{user}, signIns.checked)
	})
}

// TestSignIn_AccountChangedMeanwhile: both ways in answer a sign-in the store
// refuses as one with a wrong password, and note it as failed.
func TestSignIn_AccountChangedMeanwhile(t *testing.T) {
	for _, role := range []string{roleAdmin, roleViewer, roleDriver} {
		user := sessionAccount(t, role)
		t.Run("API: "+role, func(t *testing.T) {
			tokens, noter, users := newFakeRefreshTokens(), &notedSignIns{}, &mockUserStore{user: user}
			tokens.createErr = ErrAccountChangedMeanwhile
			w := postLogin(handleLogin(users, tokens, noter, testSecret, testTTLs, nil, false), user.Email, sessionPassword)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.JSONEq(t, `{"error":"invalid email or password"}`, w.Body.String())
			assert.Equal(t, []failedSignIn{{userID: 9, ip: "192.0.2.1", via: auditViaAPI}}, noter.noted)
			assert.Empty(t, users.seen, "not a sign-in")
		})
		if role == roleDriver {
			continue // refused before it is signed in
		}
		t.Run("admin: "+role, func(t *testing.T) {
			ui, noter := newTestAdminUI(t), &notedSignIns{}
			users := &fakeUserFetcher{users: map[string]*User{user.Email: user}}
			ui.users, ui.failedSignIns, ui.signIns = users, noter, &fakeSignIns{err: ErrAccountChangedMeanwhile}
			w := postAdminLogin(t, ui, user.Email)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Contains(t, w.Body.String(), "Invalid email or password.")
			assert.Empty(t, w.Result().Cookies(), "no session")
			assert.Equal(t, []failedSignIn{{userID: 9, ip: "203.0.113.5", via: auditViaAdmin}}, noter.noted)
			assert.Empty(t, users.seen, "not a sign-in")
		})
	}
}
