package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/OneBusAway/vehicle-positions/rider"
)

const sessionPassword = "password123"

// sessionAccount is an active account with sessionPassword in role.
func sessionAccount(t *testing.T, role string) *User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(sessionPassword), bcrypt.MinCost)
	require.NoError(t, err)
	return &User{ID: 9, Email: role + "@test.com", PasswordHash: string(hash), Role: role, Active: true}
}

// auditAddresses are the addresses a request sentThroughProxy is recorded
// with, without proxy headers trusted and with.
var auditAddresses = []struct {
	name       string
	trustProxy bool
	want       string
}{
	{"the connection's address", false, "203.0.113.5"},
	{"the proxy's word for it when proxy headers are trusted", true, "198.51.100.9"},
}

func sentThroughProxy(req *http.Request) *http.Request {
	req.RemoteAddr = "203.0.113.5:4100"
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	return req
}

// sessionActors is who a role's sign-in or sign-out is recorded against: the
// account itself for one that may use the admin UI, and no one for a driver.
func sessionActors(role, ip, via string) []auditActor {
	if role == roleDriver {
		return nil
	}
	return []auditActor{{UserID: 9, Email: role + "@test.com", IP: ip, Via: via}}
}

// fakeSignIns is a SignInRecorder that keeps who it was told signed in, as their
// sign-in checked them, and says they did at at, or fails with err.
type fakeSignIns struct {
	err     error
	at      time.Time
	actors  []auditActor
	checked []*User
}

func (f *fakeSignIns) RecordSignIn(ctx context.Context, user *User) (time.Time, error) {
	actor, _ := sessionActor(ctx)
	f.actors = append(f.actors, actor)
	f.checked = append(f.checked, user)
	if f.at.IsZero() {
		return time.Now(), f.err
	}
	return f.at, f.err
}

// TestSessions_RecordedForAccountsThatMayUseTheAdmin: each way in and out
// names the account to record and the address it came from, for an admin and
// a viewer, and does its work for a driver with no one to record.
func TestSessions_RecordedForAccountsThatMayUseTheAdmin(t *testing.T) {
	for _, role := range []string{roleAdmin, roleViewer, roleDriver} {
		for _, address := range auditAddresses {
			name := role + ", " + address.name
			t.Run("API sign-in: "+name, func(t *testing.T) {
				user := sessionAccount(t, role)
				tokens := newFakeRefreshTokens()
				body := strings.NewReader(`{"email":"` + user.Email + `","password":"` + sessionPassword + `"}`)
				w := httptest.NewRecorder()
				handleLogin(&mockUserStore{user: user}, tokens, &noopStore{}, testSecret, testTTLs, nil, address.trustProxy)(w,
					sentThroughProxy(httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)))

				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				assert.Equal(t, 1, tokens.createCalls)
				assert.Equal(t, sessionActors(role, address.want, auditViaAPI), tokens.signIns)
			})

			t.Run("API sign-out: "+name, func(t *testing.T) {
				token, err := generateJWT(sessionAccount(t, role), testSecret, defaultAccessTokenTTL)
				require.NoError(t, err)
				revocations := newFakeRevocations()
				w := httptest.NewRecorder()
				handleLogout(revocations, newFakeRefreshTokens(), address.trustProxy)(w, sentThroughProxy(logoutRequest(t, token)))

				require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
				assert.Equal(t, 1, revocations.revokeCalls)
				assert.Equal(t, sessionActors(role, address.want, auditViaAPI), revocations.signOuts)
			})

			t.Run("admin sign-in: "+name, func(t *testing.T) {
				ui := newTestAdminUI(t)
				ui.cfg.trustProxy = address.trustProxy
				user := sessionAccount(t, role)
				ui.users = &fakeUserFetcher{users: map[string]*User{user.Email: user}}
				signIns := &fakeSignIns{}
				ui.signIns = signIns

				w := postAdminLogin(t, ui, user.Email)

				assert.Equal(t, sessionActors(role, address.want, auditViaAdmin), signIns.actors)
				if role == roleDriver {
					assert.Equal(t, http.StatusForbidden, w.Code, "a driver may not sign in there")
					return
				}
				assert.Equal(t, http.StatusSeeOther, w.Code)
			})

			t.Run("admin sign-out: "+name, func(t *testing.T) {
				ui := newTestAdminUI(t)
				ui.cfg.trustProxy = address.trustProxy
				revocations := newFakeRevocations()
				ui.tokenRevoker = revocations

				w := postAdminLogout(t, ui, cookieFor(t, role))

				require.Equal(t, http.StatusSeeOther, w.Code)
				assert.Equal(t, 1, revocations.revokeCalls)
				assert.Equal(t, sessionActors(role, address.want, auditViaAdmin), revocations.signOuts)
			})
		}
	}
}

func postAdminLogin(t *testing.T, ui *adminUI, email string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	form := url.Values{"email": {email}, "password": {sessionPassword}}
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, sentThroughProxy(req))
	return w
}

func postAdminLogout(t *testing.T, ui *adminUI, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, sentThroughProxy(req))
	return w
}

// TestSessions_FailWithoutTheirRow: a sign-in whose row can't be written
// hands out nothing to be signed in with.
func TestSessions_FailWithoutTheirRow(t *testing.T) {
	for _, role := range []string{roleAdmin, roleViewer} {
		t.Run("admin sign-in: "+role, func(t *testing.T) {
			ui := newTestAdminUI(t)
			user := sessionAccount(t, role)
			users := &fakeUserFetcher{users: map[string]*User{user.Email: user}}
			ui.users = users
			ui.signIns = &fakeSignIns{err: errors.New("database unavailable")}

			w := postAdminLogin(t, ui, user.Email)

			assert.Equal(t, http.StatusInternalServerError, w.Code)
			assert.Contains(t, w.Body.String(), "Something went wrong. Try again.")
			assert.NotContains(t, w.Body.String(), "database unavailable")
			assert.Empty(t, w.Result().Cookies(), "no session")
			assert.Empty(t, users.seen, "not a sign-in")
		})

		t.Run("API sign-in: "+role, func(t *testing.T) {
			user := sessionAccount(t, role)
			tokens := newFakeRefreshTokens()
			tokens.createErr = errors.New("record session 9 sign_in: database unavailable")

			w := postLogin(handleLogin(&mockUserStore{user: user}, tokens, &noopStore{}, testSecret, testTTLs, nil, false), user.Email, sessionPassword)

			assert.Equal(t, http.StatusInternalServerError, w.Code)
			assert.JSONEq(t, `{"error":"internal server error"}`, w.Body.String(), "no tokens")
		})
	}
}

// TestSessions_NotRecorded: what isn't a sign-in, or ends no session, names
// no account to record.
func TestSessions_NotRecorded(t *testing.T) {
	inactive := sessionAccount(t, roleAdmin)
	inactive.Active = false
	for _, tc := range []struct {
		name     string
		user     *User
		password string
	}{
		{"a wrong password", sessionAccount(t, roleAdmin), "nope"},
		{"a deactivated account", inactive, sessionPassword},
	} {
		t.Run("API sign-in: "+tc.name, func(t *testing.T) {
			tokens := newFakeRefreshTokens()
			w := postLogin(handleLogin(&mockUserStore{user: tc.user}, tokens, &noopStore{}, testSecret, testTTLs, nil, false), tc.user.Email, tc.password)
			require.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Zero(t, tokens.createCalls)
			assert.Empty(t, tokens.signIns)
		})

		t.Run("admin sign-in: "+tc.name, func(t *testing.T) {
			ui := newTestAdminUI(t)
			ui.users = &fakeUserFetcher{users: map[string]*User{tc.user.Email: tc.user}}
			signIns := &fakeSignIns{}
			ui.signIns = signIns
			mux := http.NewServeMux()
			registerAdminUI(mux, ui)
			form := url.Values{"email": {tc.user.Email}, "password": {tc.password}}
			req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			require.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Empty(t, signIns.actors)
		})
	}

	t.Run("admin sign-out: an invalid cookie", func(t *testing.T) {
		ui := newTestAdminUI(t)
		revocations := newFakeRevocations()
		ui.tokenRevoker = revocations

		w := postAdminLogout(t, ui, &http.Cookie{Name: sessionCookieName, Value: "garbage"})

		require.Equal(t, http.StatusSeeOther, w.Code)
		assert.Empty(t, revocations.signOuts)
	})
}

// TestAuditPage_SessionRows checks a sign-in and a sign-out read as one, name
// the account as it is called now, and say which way they went.
func TestAuditPage_SessionRows(t *testing.T) {
	ana, gone := int64(7), int64(8)
	at := time.Date(2026, 11, 24, 14, 5, 0, 0, time.UTC)
	log := &fakeAuditLog{entries: []auditEntry{
		{
			ID: 3, At: at, ActorID: &ana, ActorEmail: "ana@example.org", ActorIP: "198.51.100.9",
			ObjectType: auditSession, ObjectID: "7", Action: auditSignOut, AccountName: "Ana Diaz",
			After: fieldsJSON(t, sessionAudit{Via: auditViaAdmin}),
		},
		{
			ID: 2, At: at, ActorID: &ana, ActorEmail: "ana@example.org", ActorIP: "198.51.100.9",
			ObjectType: auditSession, ObjectID: "7", Action: auditSignIn, AccountName: "Ana Diaz",
			After: fieldsJSON(t, sessionAudit{Via: auditViaAdmin}),
		},
		{
			ID: 1, At: at, ActorID: &gone, ActorEmail: "gone@example.org", ActorIP: "198.51.100.10",
			ObjectType: auditSession, ObjectID: "8", Action: auditSignIn,
			After: fieldsJSON(t, sessionAudit{Via: auditViaAPI}),
		},
	}}
	body := getAdminPage(t, newAuditMux(t, log), "/admin/audit?type=session")

	assert.Contains(t, body, `Sign-out by <a href="/admin/audit?id=7&amp;type=session">Ana Diaz</a>`)
	assert.Contains(t, body, `Sign-in by <a href="/admin/audit?id=7&amp;type=session">Ana Diaz</a>`)
	assert.Contains(t, body, `<span class="muted">Through</span> Admin site`)
	assert.Contains(t, body, `<a href="/admin/audit?actor=7">ana@example.org</a><span class="sub num">198.51.100.9</span>`)

	assert.Contains(t, body, `Sign-in by <a href="/admin/audit?id=8&amp;type=session">account 8</a>`, "by its number once it is deleted")
	assert.Contains(t, body, `<span class="muted">Through</span> API`)
	assert.Contains(t, body, "every sign-in and sign-out by an admin or a viewer")
}

// TestAudit_SessionRowsHideTheAddressFromAViewer: a viewer sees who signed in
// and which way, on the page and in the API, and never from where.
func TestAudit_SessionRowsHideTheAddressFromAViewer(t *testing.T) {
	ana := int64(7)
	log := &fakeAuditLog{entries: []auditEntry{{
		ID: 2, At: time.Now(), ActorID: &ana, ActorEmail: "ana@example.org", ActorIP: "203.0.113.24",
		ObjectType: auditSession, ObjectID: "7", Action: auditSignIn, AccountName: "Ana Diaz",
		After: fieldsJSON(t, sessionAudit{Via: auditViaAdmin}),
	}}}
	mux := newAuditMux(t, log)
	page := func(role string) string {
		req := httptest.NewRequest(http.MethodGet, "/admin/audit", nil)
		req.AddCookie(cookieFor(t, role))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}

	for _, body := range []string{page(roleViewer), getAuditAPI(t, log, roleViewer, "").Body.String()} {
		assert.Contains(t, body, "ana@example.org")
		assert.NotContains(t, body, "203.0.113.24")
	}
	for _, body := range []string{page(roleAdmin), getAuditAPI(t, log, roleAdmin, "").Body.String()} {
		assert.Contains(t, body, "203.0.113.24")
	}
}

// TestStore_AuditLog_SignOutRecordedOnce: the second sign-out of a token adds
// nothing to the revocation list, so it records nothing either, whether it
// comes after the first or at the same time.
func TestStore_AuditLog_SignOutRecordedOnce(t *testing.T) {
	store := newAuditTestStore(t)
	fx := newAuditFixture(t, store)
	ctx := signingThrough(asTestAdmin(), auditViaAPI)
	signOut := func(jti string) error {
		return store.RevokeToken(ctx, jti, fx.userID, time.Now().Add(time.Hour))
	}

	t.Run("one after the other", func(t *testing.T) {
		mark := lastAuditID(t, store)
		require.NoError(t, signOut("aud-twice"))
		require.NoError(t, signOut("aud-twice"))
		rows := auditRowsAfter(t, store, mark)
		require.Len(t, rows, 1)
		assert.Equal(t, auditSignOut, rows[0].Action)
	})

	t.Run("at the same time", func(t *testing.T) {
		mark := lastAuditID(t, store)
		const tabs = 6
		errs := make(chan error, tabs)
		for range tabs {
			go func() { errs <- signOut("aud-tabs") }()
		}
		for range tabs {
			require.NoError(t, <-errs)
		}
		assert.Len(t, auditRowsAfter(t, store, mark), 1)
	})
}

// TestStore_AuditLog_OtherSessionsRecordNothing: a driver signs in and out
// with no row, and so even while none can be written. Neither does an actor
// that is making a change, not signing in or out.
func TestStore_AuditLog_OtherSessionsRecordNothing(t *testing.T) {
	store := newAuditTestStore(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	driver := &User{ID: 7, Email: "aud-ana@example.org", Role: roleDriver}
	for name, ctx := range map[string]context.Context{
		"a driver":                 withSessionActor(req, driver, auditViaAPI, false),
		"an admin making a change": asTestAdmin(),
	} {
		t.Run(name, func(t *testing.T) {
			fx := newAuditFixture(t, store)
			failWrites(t, store, "BEFORE INSERT", "audit_log", "true", `RAISE EXCEPTION 'test failure';`)
			mark := lastAuditID(t, store)

			_, err := store.CreateRefreshToken(ctx, "aud-hash", checkedUser(t, store, fx.userID), time.Now().Add(time.Hour))
			require.NoError(t, err)
			require.NoError(t, store.RevokeToken(ctx, "aud-jti", fx.userID, time.Now().Add(time.Hour)))

			assert.Empty(t, auditRowsAfter(t, store, mark))
			assert.Equal(t, 1, countRefreshRows(t, store, "aud-hash"))
			assert.Equal(t, 1, countRevocationRows(t, store, "aud-jti"))
		})
	}
}

// TestStore_AuditLog_RenewalRecordsNothing: a token renewed is not a sign-in,
// even by an account whose sign-ins are recorded.
func TestStore_AuditLog_RenewalRecordsNothing(t *testing.T) {
	store := newAuditTestStore(t)
	fx := newAuditFixture(t, store)
	ctx := signingThrough(asTestAdmin(), auditViaAPI)
	_, err := store.CreateRefreshToken(ctx, "aud-first", checkedUser(t, store, fx.userID), time.Now().Add(time.Hour))
	require.NoError(t, err)
	first, err := store.GetRefreshToken(ctx, "aud-first")
	require.NoError(t, err)
	mark := lastAuditID(t, store)

	_, rotated, err := store.RotateRefreshToken(ctx, first.ID, "aud-second", fx.userID, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.True(t, rotated)

	assert.Empty(t, auditRowsAfter(t, store, mark))
}

// TestAuditLog_SessionsThroughTheServer is the whole path against the
// database: an admin, a viewer and a driver sign in and out every way they
// can, and the log has a row for each time the first two did.
func TestAuditLog_SessionsThroughTheServer(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := context.Background()
	accounts := make(map[string]*UserResponse)
	for role, name := range map[string]string{roleAdmin: "Ana Diaz", roleViewer: "Vic Viewer", roleDriver: "Dee Driver"} {
		u, err := store.CreateUser(ctx, name, "aud-"+role+"@example.org", sessionPassword, role)
		require.NoError(t, err)
		accounts[role] = u
	}

	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return nil }, testReportSettings)
	h, err := newHandler(serverStore{store, &noopStore{}}, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, trustProxy: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, nil, nil,
		newAlertWatcher(store, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)
	serve := func(method, path, body string, set func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-Forwarded-For", "198.51.100.41")
		if strings.HasPrefix(path, "/admin/") {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if set != nil {
			set(req)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	mark := lastAuditID(t, store)
	for _, role := range []string{roleAdmin, roleViewer, roleDriver} {
		// The API: sign in, and out twice with the same token. The second is
		// refused, as a revoked token always is.
		w := serve(http.MethodPost, "/api/v1/auth/login", `{"email":"aud-`+role+`@example.org","password":"`+sessionPassword+`"}`, nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var login LoginResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&login))
		bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+login.AccessToken) }
		require.Equal(t, http.StatusNoContent, serve(http.MethodPost, "/api/v1/auth/logout", "", bearer).Code)
		require.Equal(t, http.StatusUnauthorized, serve(http.MethodPost, "/api/v1/auth/logout", "", bearer).Code)

		// The admin site: sign in, and out twice with the same cookie.
		w = serve(http.MethodPost, "/admin/login", "email=aud-"+role+"%40example.org&password="+sessionPassword, nil)
		if role == roleDriver {
			require.Equal(t, http.StatusForbidden, w.Code)
			continue
		}
		require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
		require.Len(t, w.Result().Cookies(), 1)
		session := w.Result().Cookies()[0]
		for range 2 {
			w = serve(http.MethodPost, "/admin/logout", "", func(r *http.Request) { r.AddCookie(session) })
			require.Equal(t, "/admin/login", w.Header().Get("Location"))
		}
	}

	got := make([]string, 0)
	for _, r := range auditRowsAfter(t, store, mark) {
		require.NotNil(t, r.ActorID)
		assert.Equal(t, auditSession, r.ObjectType)
		assert.Equal(t, idText(*r.ActorID), r.ObjectID, "the row is about the account it is by")
		assert.Equal(t, "198.51.100.41", r.ActorIP)
		assert.Empty(t, r.Before)
		got = append(got, r.ActorEmail+" "+r.Action+" "+r.After)
	}
	assert.Equal(t, []string{
		`aud-admin@example.org sign_in {"via": "api"}`,
		`aud-admin@example.org sign_out {"via": "api"}`,
		`aud-admin@example.org sign_in {"via": "admin"}`,
		`aud-admin@example.org sign_out {"via": "admin"}`,
		`aud-viewer@example.org sign_in {"via": "api"}`,
		`aud-viewer@example.org sign_out {"via": "api"}`,
		`aud-viewer@example.org sign_in {"via": "admin"}`,
		`aud-viewer@example.org sign_out {"via": "admin"}`,
	}, got)

	// The Audit log names the account, as it is called now.
	_, err = store.UpdateUser(ctx, accounts[roleAdmin].ID, "Ana Maria Diaz", "aud-admin@example.org", roleAdmin)
	require.NoError(t, err)
	id := idText(accounts[roleAdmin].ID)
	session, err := generateJWT(&User{ID: accounts[roleAdmin].ID, Email: "aud-admin@example.org", Role: roleAdmin}, testSecret, sessionLifetime)
	require.NoError(t, err)
	w := serve(http.MethodGet, "/admin/audit?type=session&id="+id, "", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	})
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Sign-ins for Ana Maria Diaz")
	assert.Contains(t, w.Body.String(), `Sign-in by <a href="/admin/audit?id=`+id+`&amp;type=session">Ana Maria Diaz</a>`)
	assert.NotContains(t, w.Body.String(), "Vic Viewer")

	// Deleted, its rows stay and no longer have a name to show.
	require.NoError(t, store.DeleteUser(ctx, accounts[roleAdmin].ID))
	entries, err := store.ListAuditEntries(ctx, auditFilter{ObjectType: auditSession, ObjectID: id, Limit: 10})
	require.NoError(t, err)
	require.Len(t, entries, 4)
	for _, e := range entries {
		assert.Empty(t, e.AccountName)
		assert.Equal(t, "aud-admin@example.org", e.ActorEmail)
	}
}
