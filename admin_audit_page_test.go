package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// fakeAuditLog is an AuditLogLister over fixed entries that records the
// filter it was asked for.
type fakeAuditLog struct {
	entries []auditEntry
	asked   []auditFilter
}

func (f *fakeAuditLog) ListAuditEntries(_ context.Context, filter auditFilter) ([]auditEntry, error) {
	f.asked = append(f.asked, filter)
	return f.entries, nil
}

func newAuditMux(t *testing.T, log AuditLogLister) *http.ServeMux {
	t.Helper()
	ui := newTestAdminUI(t)
	ui.auditLog = log
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	return mux
}

func fieldsJSON(t *testing.T, fields any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(fields)
	require.NoError(t, err)
	return b
}

func TestWithAuditActor(t *testing.T) {
	claims := jwt.MapClaims{"sub": "7", "email": "ops@example.org", "role": roleAdmin}
	tests := []struct {
		name       string
		trustProxy bool
		wantIP     string
	}{
		{"the connection's address", false, "203.0.113.5"},
		{"the last proxy hop when proxy headers are trusted", true, "198.51.100.2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/vehicles", nil)
			req.RemoteAddr = "203.0.113.5:4100"
			req.Header.Set("X-Forwarded-For", "198.51.100.1, 198.51.100.2")

			got, err := withAuditActor(req, claims, tc.trustProxy)
			require.NoError(t, err)

			actor, ok := got.Context().Value(auditActorKey).(auditActor)
			require.True(t, ok)
			assert.Equal(t, auditActor{UserID: 7, Email: "ops@example.org", IP: tc.wantIP}, actor)
		})
	}

	t.Run("a subject that is not a user id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/vehicles", nil)
		_, err := withAuditActor(req, jwt.MapClaims{"sub": "ops", "role": roleAdmin}, false)
		require.ErrorContains(t, err, `token subject "ops" is not a user id`)
	})
}

// actorRecorder is a handler that keeps the audit actor its request carried.
type actorRecorder struct {
	actor  auditActor
	called bool
}

func (a *actorRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.called = true
	a.actor, _ = r.Context().Value(auditActorKey).(auditActor)
	w.WriteHeader(http.StatusNoContent)
}

// TestRequireAdmin_NamesTheAuditActor checks the JSON API's admin gate hands
// the admin and their address to the handler behind it, which is how every
// admin change made through the API is recorded against them.
func TestRequireAdmin_NamesTheAuditActor(t *testing.T) {
	token, err := generateJWT(&User{ID: 7, Email: "ops@example.org", Role: roleAdmin}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)
	next := &actorRecorder{}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/vehicles/v1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	w := httptest.NewRecorder()
	requireAuth(testSecret, newFakeRevocations())(requireAdmin(true)(next)).ServeHTTP(w, req)

	require.True(t, next.called)
	assert.Equal(t, auditActor{UserID: 7, Email: "ops@example.org", IP: "198.51.100.9"}, next.actor)
}

// TestRequireAdmin_RefusesAnActorThatIsNoUser checks a request whose claims
// name no user id is refused rather than recorded against no one. requireAuth
// refuses such a token first, so this is a token that came some other way.
func TestRequireAdmin_RefusesAnActorThatIsNoUser(t *testing.T) {
	next := &actorRecorder{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/vehicles/v1", nil)
	req = req.WithContext(contextWithClaims(req.Context(), jwt.MapClaims{"sub": "ops", "role": roleAdmin}))
	w := httptest.NewRecorder()

	requireAdmin(false)(next).ServeHTTP(w, req)

	assert.False(t, next.called)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.JSONEq(t, `{"error":"unauthorized"}`, w.Body.String())
}

// TestRequireAdminPage_NamesTheAuditActor is the same check for the admin
// pages' gate.
func TestRequireAdminPage_NamesTheAuditActor(t *testing.T) {
	next := &actorRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/admin/vehicles", nil)
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()

	requireAdminPage(testSecret, newFakeRevocations(), false)(next).ServeHTTP(w, req)

	require.True(t, next.called)
	// httptest's requests come from 192.0.2.1.
	assert.Equal(t, auditActor{UserID: 9, Email: "admin@test.com", IP: "192.0.2.1"}, next.actor)
}

func TestAuditDetails(t *testing.T) {
	vehicle := map[string]any{"label": "Old", "agency_tag": "", "active": true}
	tests := []struct {
		name          string
		before, after map[string]any
		want          []auditDetail
	}{
		{
			name:  "a create lists every field it set",
			after: vehicle,
			want:  []auditDetail{{Label: "Label", To: "Old"}, {Label: "Agency tag", To: "none"}, {Label: "Status", To: "Active"}},
		},
		{
			name:   "a delete lists every field it removed",
			before: map[string]any{"name": "Ana", "email": "ana@example.org", "role": "driver", "active": false},
			want:   []auditDetail{{Label: "Name", From: "Ana"}, {Label: "Email", From: "ana@example.org"}, {Label: "Role", From: "Driver"}, {Label: "Status", From: "Deactivated"}},
		},
		{
			name:   "an edit lists only the fields it changed",
			before: vehicle,
			after:  map[string]any{"label": "New", "agency_tag": "", "active": false},
			want:   []auditDetail{{Label: "Label", From: "Old", To: "New"}, {Label: "Status", From: "Active", To: "Deactivated"}},
		},
		{
			name:   "a trip's status in words",
			before: map[string]any{"status": "active"},
			after:  map[string]any{"status": "completed"},
			want:   []auditDetail{{Label: "Status", From: "Running", To: "Ended"}},
		},
		{
			name:  "a sign-in to the admin site",
			after: map[string]any{"via": "admin"},
			want:  []auditDetail{{Label: "Through", To: "Admin site"}},
		},
		{
			name:  "a sign-in through the API",
			after: map[string]any{"via": "api"},
			want:  []auditDetail{{Label: "Through", To: "API"}},
		},
		{
			name:  "an assignment names the vehicle",
			after: map[string]any{"vehicle_id": "v-12"},
			want:  []auditDetail{{Label: "Vehicle", To: "v-12"}},
		},
		{
			name: "a new password has none",
			want: []auditDetail{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, auditDetails(tc.before, tc.after))
		})
	}
}

// TestAuditPage_Rows checks each row says what changed, to what, by whom and
// from where, and links to the object's history and the admin's changes.
func TestAuditPage_Rows(t *testing.T) {
	admin := int64(7)
	at := time.Date(2026, 11, 20, 14, 5, 0, 0, time.UTC)
	log := &fakeAuditLog{entries: []auditEntry{
		{
			ID: 3, At: at, ActorID: &admin, ActorEmail: "ops@example.org", ActorIP: "198.51.100.9",
			ObjectType: auditVehicle, ObjectID: "v-12", Action: auditUpdate,
			Before: fieldsJSON(t, vehicleAudit{Label: "Old", AgencyTag: "north", Active: true}),
			After:  fieldsJSON(t, vehicleAudit{Label: "Renamed", AgencyTag: "north", Active: true}),
		},
		{
			ID: 2, At: at, ObjectType: auditUser, ObjectID: "5", Action: auditCreate,
			After: fieldsJSON(t, userAudit{Name: "Administrator", Email: "boot@example.org", Role: roleAdmin, Active: true}),
		},
		{
			ID: 1, At: at, ActorID: &admin, ActorEmail: "ops@example.org", ActorIP: "198.51.100.9",
			ObjectType: auditUser, ObjectID: "6", Action: auditPassword,
		},
		{
			ID: 1, At: at, ActorID: &admin, ActorEmail: "ops@example.org", ActorIP: "198.51.100.9",
			ObjectType: auditUser, ObjectID: "7", Action: auditPassword, AccountName: "Joseph Kamau",
		},
		{
			ID: 0, At: at, ActorID: &admin, ActorEmail: "ops@example.org", ActorIP: "198.51.100.9",
			ObjectType: auditTrip, ObjectID: "12", Action: auditEnd,
			Before: fieldsJSON(t, tripAudit{Status: "active"}), After: fieldsJSON(t, tripAudit{Status: "completed"}),
		},
	}}
	body := getAdminPage(t, newAuditMux(t, log), "/admin/audit")

	assert.Contains(t, body, `Edited vehicle <a href="/admin/audit?id=v-12&amp;type=vehicle">Renamed</a>`)
	assert.Contains(t, body, `<span class="muted">Label</span> Old &rarr; Renamed`)
	assert.NotContains(t, body, `<span class="muted">Agency tag</span>`, "an unchanged field is not listed")
	assert.Contains(t, body, `<a href="/admin/audit?actor=7">ops@example.org</a><span class="sub num">198.51.100.9</span>`)
	assert.Contains(t, body, `14:05<span class="sub">20 Nov</span>`, "times are on the page's clock, UTC with no schedule")

	assert.Contains(t, body, `Created account <a href="/admin/audit?id=5&amp;type=user">Administrator</a>`)
	assert.Contains(t, body, `<span class="muted">Server</span>`, "a change the server made has no admin")

	assert.Contains(t, body, `Set a new password for <a href="/admin/audit?id=7&amp;type=user">Joseph Kamau</a>`, "a row with no fields names the account as it is called now")
	assert.Contains(t, body, `Set a new password for <a href="/admin/audit?id=6&amp;type=user">account 6</a>`, "and by its id once it is deleted")

	assert.Contains(t, body, `Ended trip <a href="/admin/audit?id=12&amp;type=trip">12</a>`)
	assert.Contains(t, body, `<span class="muted">Status</span> Running &rarr; Ended`)
}

// TestAuditPage_Filters checks the page asks the store for what its query
// names, a page at a time, and refuses a filter that names nothing.
func TestAuditPage_Filters(t *testing.T) {
	t.Run("narrowed and paged", func(t *testing.T) {
		log := &fakeAuditLog{}
		getAdminPage(t, newAuditMux(t, log), "/admin/audit?type=vehicle&id=v-12&actor=7&page=2")

		require.Len(t, log.asked, 1)
		got := log.asked[0]
		require.NotNil(t, got.ActorID)
		assert.Equal(t, int64(7), *got.ActorID)
		got.ActorID = nil
		assert.Equal(t, auditFilter{ObjectType: auditVehicle, ObjectID: "v-12", Limit: adminPageSize + 1, Offset: adminPageSize}, got)
	})

	t.Run("unfiltered", func(t *testing.T) {
		log := &fakeAuditLog{}
		getAdminPage(t, newAuditMux(t, log), "/admin/audit")
		require.Len(t, log.asked, 1)
		assert.Equal(t, auditFilter{Limit: adminPageSize + 1}, log.asked[0])
	})

	for query, want := range map[string]string{
		"type=driver": "type must be vehicle, user, trip, api_key or session",
		"id=v-12":     "id needs a type",
		"actor=ops":   "actor must be a user id",
		"actor=0":     "actor must be a user id",
		"actor=-7":    "actor must be a user id",
	} {
		t.Run("refuses "+query, func(t *testing.T) {
			log := &fakeAuditLog{}
			w := requestAdminPage(t, newAuditMux(t, log), "/admin/audit?"+query)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), want)
			assert.Empty(t, log.asked, "nothing is read for a bad filter")
		})
	}
}

// TestAuditPage_HeadlineAndChips checks the headline names what is shown and
// the chip for it is marked: a chip shows a whole kind, so none is marked for
// one object's history or one admin's changes.
func TestAuditPage_HeadlineAndChips(t *testing.T) {
	admin := int64(7)
	vehicleRow := auditEntry{
		ID: 1, At: time.Now(), ActorID: &admin, ActorEmail: "ops@example.org",
		ObjectType: auditVehicle, ObjectID: "v-12", Action: auditDeactivate,
		Before: fieldsJSON(t, vehicleAudit{Label: "Van 12", Active: true}),
		After:  fieldsJSON(t, vehicleAudit{Label: "Van 12", Active: false}),
	}
	signInRow := auditEntry{
		ID: 3, At: time.Now(), ActorID: &admin, ActorEmail: "ops@example.org", AccountName: "Ana Diaz",
		ObjectType: auditSession, ObjectID: "7", Action: auditSignIn, After: fieldsJSON(t, sessionAudit{Via: auditViaAdmin}),
	}
	tests := []struct {
		query    string
		entries  []auditEntry
		headline string
		current  string
	}{
		{"", []auditEntry{vehicleRow}, "Who changed what", "All"},
		{"?type=vehicle", []auditEntry{vehicleRow}, "Who changed what", "Vehicles"},
		{"?type=vehicle&id=v-12", []auditEntry{vehicleRow}, "History of Van 12", ""},
		{"?type=vehicle&id=v-99", nil, "History of vehicle v-99", ""},
		{"?type=trip&id=12", []auditEntry{{ID: 2, At: time.Now(), ObjectType: auditTrip, ObjectID: "12", Action: auditEnd}}, "History of trip 12", ""},
		{"?actor=7", []auditEntry{vehicleRow}, "What ops@example.org did", ""},
		{"?actor=8", nil, "What account 8 did", ""},
		{"?type=session", []auditEntry{signInRow}, "Who signed in", "Sign-ins"},
		{"?type=session&id=7", []auditEntry{signInRow}, "Sign-ins for Ana Diaz", ""},
		{"?type=session&id=9", nil, "Sign-ins for account 9", ""},
		{"?type=session&actor=7", []auditEntry{signInRow}, "What ops@example.org did", ""},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			body := getAdminPage(t, newAuditMux(t, &fakeAuditLog{entries: tc.entries}), "/admin/audit"+tc.query)
			assert.Contains(t, body, `<h2 class="h1">`+tc.headline+`</h2>`)
			for _, label := range []string{"All", "Vehicles", "Accounts", "Trips", "API keys", "Sign-ins"} {
				isCurrent := strings.Contains(body, `aria-current="true">`+label+`</a>`)
				assert.Equal(t, label == tc.current, isCurrent, "chip %s", label)
			}
		})
	}
}

// TestAuditPage_EmptyAndPaged checks the empty state, and that a full page
// links to the next with the filter kept.
func TestAuditPage_EmptyAndPaged(t *testing.T) {
	t.Run("nothing yet", func(t *testing.T) {
		body := getAdminPage(t, newAuditMux(t, &fakeAuditLog{}), "/admin/audit")
		assert.Contains(t, body, "Nothing yet")
	})

	t.Run("nothing for a filter", func(t *testing.T) {
		body := getAdminPage(t, newAuditMux(t, &fakeAuditLog{}), "/admin/audit?type=trip")
		assert.Contains(t, body, "Nothing here")
	})

	t.Run("a full page", func(t *testing.T) {
		entries := make([]auditEntry, adminPageSize+1)
		for i := range entries {
			entries[i] = auditEntry{ID: int64(100 - i), At: time.Now(), ObjectType: auditTrip, ObjectID: "1", Action: auditEnd}
		}
		body := getAdminPage(t, newAuditMux(t, &fakeAuditLog{entries: entries}), "/admin/audit?type=trip")
		assert.Equal(t, adminPageSize, strings.Count(body, "Ended trip"), "one row past the page is read, not shown")
		assert.Contains(t, body, "50 entries shown, page 1")
		assert.Contains(t, body, `href="/admin/audit?page=2&amp;type=trip"`)
	})
}

// TestAdminSidebar_LinksToAuditLog checks the rail has the page on every admin
// page and marks it open only on its own.
func TestAdminSidebar_LinksToAuditLog(t *testing.T) {
	mux := newAuditMux(t, &fakeAuditLog{})
	for path, want := range map[string]railState{
		"/admin/audit":     railOpen,
		"/admin/dashboard": railLinked,
	} {
		t.Run(path, func(t *testing.T) {
			body := getAdminPage(t, mux, path)
			assert.Equal(t, want, railLink(body, "/admin/audit"))
			assert.Contains(t, body, `<span class="rail-label">Audit log</span>`)
		})
	}
}

// TestAuditLog_ThroughTheServer is the whole path against the database: an
// admin changes a vehicle through the JSON API and through the admin pages,
// each change is recorded against them with the address their request came
// from, and the Audit log page shows both.
func TestAuditLog_ThroughTheServer(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := context.Background()
	admin, err := store.CreateUser(ctx, "Ops", "aud-ops@example.org", "a-password", roleAdmin)
	require.NoError(t, err)
	token, err := generateJWT(&User{ID: admin.ID, Email: admin.Email, Role: roleAdmin}, testSecret, defaultAccessTokenTTL)
	require.NoError(t, err)
	session, err := generateJWT(&User{ID: admin.ID, Email: admin.Email, Role: roleAdmin}, testSecret, sessionLifetime)
	require.NoError(t, err)

	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return nil }, testReportSettings)
	h, err := newHandler(serverStore{store, &noopStore{}}, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, trustProxy: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, nil, nil,
		newAlertWatcher(store, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/vehicles", strings.NewReader(`{"id":"aud-api","label":"From the API"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-For", "198.51.100.21")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	req = httptest.NewRequest(http.MethodPost, "/admin/vehicles", strings.NewReader("id=aud-page&label=From+the+page"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", "198.51.100.22")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())

	actor := admin.ID
	entries, err := store.ListAuditEntries(ctx, auditFilter{ActorID: &actor, Limit: 10})
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, []string{"aud-page 198.51.100.22", "aud-api 198.51.100.21"},
		[]string{entries[0].ObjectID + " " + entries[0].ActorIP, entries[1].ObjectID + " " + entries[1].ActorIP})
	for _, e := range entries {
		assert.Equal(t, "aud-ops@example.org", e.ActorEmail)
		assert.Equal(t, auditCreate, e.Action)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/audit?actor="+idText(admin.ID), nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "What aud-ops@example.org did")
	assert.Contains(t, body, `Added vehicle <a href="/admin/audit?id=aud-page&amp;type=vehicle">From the page</a>`)
	assert.Contains(t, body, `Added vehicle <a href="/admin/audit?id=aud-api&amp;type=vehicle">From the API</a>`)
}

// TestAuditLog_AnAdminsNewEmail: an admin who changes their own email stays
// signed in with the session that carries the old one, and the changes they
// make are recorded, and listed, under the new one.
func TestAuditLog_AnAdminsNewEmail(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := context.Background()
	admin, err := store.CreateUser(ctx, "Ops", "aud-ops@example.org", "a-password", roleAdmin)
	require.NoError(t, err)
	session, err := generateJWT(&User{ID: admin.ID, Email: admin.Email, Role: roleAdmin}, testSecret, sessionLifetime)
	require.NoError(t, err)

	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return nil }, testReportSettings)
	h, err := newHandler(serverStore{store, &noopStore{}}, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, nil, nil,
		newAlertWatcher(store, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)
	serve := func(method, path, form string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	w := serve(http.MethodPost, "/admin/users/"+idText(admin.ID), "name=Ops&email=aud-ops-new%40example.org&role=admin")
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	w = serve(http.MethodPost, "/admin/vehicles", "id=aud-page&label=After")
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String(), "the email change left the session signed in")

	actor := admin.ID
	entries, err := store.ListAuditEntries(ctx, auditFilter{ActorID: &actor, Limit: 10})
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, []string{"aud-page", idText(admin.ID)}, []string{entries[0].ObjectID, entries[1].ObjectID})
	for _, e := range entries {
		assert.Equal(t, "aud-ops-new@example.org", e.ActorEmail)
	}

	w = serve(http.MethodGet, "/admin/audit?actor="+idText(admin.ID), "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "What aud-ops-new@example.org did")
}
