package main

import (
	"context"
	"encoding/json"
	"errors"
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

// getAuditAPI sends GET /api/v1/admin/audit with query to the handler over
// log, as role: the claims requireAuth would have put on the context.
func getAuditAPI(t *testing.T, log AuditLogLister, role, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit"+query, nil)
	req = req.WithContext(contextWithClaims(req.Context(), jwt.MapClaims{"sub": "9", "role": role}))
	w := httptest.NewRecorder()
	handleListAuditEntries(log)(w, req)
	return w
}

// auditAPIEntries is two rows as the store returns them: an admin's edit of
// a vehicle, and an account the server created, which has no actor and no
// before.
func auditAPIEntries() []auditEntry {
	actor := int64(5)
	nairobi := time.FixedZone("EAT", 3*60*60)
	return []auditEntry{
		{
			ID: 12, At: time.Date(2026, 11, 23, 11, 14, 3, 0, nairobi), ActorID: &actor,
			ActorEmail: "ops@example.org", ActorIP: "203.0.113.24",
			ObjectType: auditVehicle, ObjectID: "bus-1", Action: auditUpdate,
			Before: json.RawMessage(`{"label": "Bus One", "active": true, "agency_tag": ""}`),
			After:  json.RawMessage(`{"label": "Bus 1", "active": true, "agency_tag": ""}`),
		},
		{
			ID: 11, At: time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC),
			ObjectType: auditUser, ObjectID: "1", Action: auditCreate,
			After:       json.RawMessage(`{"name": "Ops", "role": "admin", "email": "ops@example.org", "active": true}`),
			AccountName: "Ops",
		},
	}
}

// TestListAuditEntries_Entries pins each entry's fields for an admin: the
// time in UTC, the actor or nulls for the server, the object, the action,
// and before and after as the JSON objects the log stored, null where the
// row has none.
func TestListAuditEntries_Entries(t *testing.T) {
	w := getAuditAPI(t, &fakeAuditLog{entries: auditAPIEntries()}, roleAdmin, "")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.JSONEq(t, `{
		"count": 2,
		"has_more": false,
		"entries": [
			{
				"id": 12, "at": "2026-11-23T08:14:03Z",
				"actor_id": 5, "actor_email": "ops@example.org", "actor_ip": "203.0.113.24",
				"object_type": "vehicle", "object_id": "bus-1", "action": "update",
				"before": {"label": "Bus One", "active": true, "agency_tag": ""},
				"after": {"label": "Bus 1", "active": true, "agency_tag": ""}
			},
			{
				"id": 11, "at": "2026-11-23T08:00:00Z",
				"actor_id": null, "actor_email": "", "actor_ip": "",
				"object_type": "user", "object_id": "1", "action": "create",
				"before": null,
				"after": {"name": "Ops", "role": "admin", "email": "ops@example.org", "active": true}
			}
		]
	}`, w.Body.String())
}

// TestListAuditEntries_SignIn pins a sign-in's fields: the account it is by is
// the object too, and after says which way in.
func TestListAuditEntries_SignIn(t *testing.T) {
	actor := int64(5)
	log := &fakeAuditLog{entries: []auditEntry{{
		ID: 13, At: time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC), ActorID: &actor,
		ActorEmail: "ops@example.org", ActorIP: "203.0.113.24", AccountName: "Ops",
		ObjectType: auditSession, ObjectID: "5", Action: auditSignIn, After: fieldsJSON(t, sessionAudit{Via: auditViaAPI}),
	}}}

	w := getAuditAPI(t, log, roleAdmin, "?type=session")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{
		"count": 1,
		"has_more": false,
		"entries": [{
			"id": 13, "at": "2026-11-24T08:00:00Z",
			"actor_id": 5, "actor_email": "ops@example.org", "actor_ip": "203.0.113.24",
			"object_type": "session", "object_id": "5", "action": "sign_in",
			"before": null,
			"after": {"via": "api"}
		}]
	}`, w.Body.String())
}

// TestListAuditEntries_LeavesOutTheAddressForAViewer: a viewer gets every
// entry an admin gets, without actor_ip at all, as a viewer's Audit log page
// leaves the address out.
func TestListAuditEntries_LeavesOutTheAddressForAViewer(t *testing.T) {
	log := &fakeAuditLog{entries: auditAPIEntries()}
	admin := getAuditAPI(t, log, roleAdmin, "")
	viewer := getAuditAPI(t, log, roleViewer, "")
	require.Equal(t, http.StatusOK, viewer.Code, viewer.Body.String())

	var adminBody, viewerBody struct {
		Entries []map[string]any `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(admin.Body.Bytes(), &adminBody))
	require.NoError(t, json.Unmarshal(viewer.Body.Bytes(), &viewerBody))
	require.Len(t, viewerBody.Entries, 2)
	for i, entry := range viewerBody.Entries {
		assert.NotContains(t, entry, "actor_ip")
		assert.Contains(t, adminBody.Entries[i], "actor_ip")
		delete(adminBody.Entries[i], "actor_ip")
		assert.Equal(t, adminBody.Entries[i], entry, "a viewer gets the rest of the entry as an admin does")
	}
	assert.NotContains(t, viewer.Body.String(), "203.0.113.24")
}

// TestListAuditEntries_Filters checks the API reads the Audit log page's
// filters and the admin lists' limit and offset, and asks the store for one
// row more than a page.
func TestListAuditEntries_Filters(t *testing.T) {
	actor := int64(7)
	tests := []struct {
		name  string
		query string
		want  auditFilter
	}{
		{name: "none", query: "", want: auditFilter{Limit: defaultListLimit + 1}},
		{name: "one kind of object", query: "?type=api_key", want: auditFilter{ObjectType: auditAPIKey, Limit: defaultListLimit + 1}},
		{name: "the sign-ins", query: "?type=session", want: auditFilter{ObjectType: auditSession, Limit: defaultListLimit + 1}},
		{name: "one account's sign-ins", query: "?type=session&id=5", want: auditFilter{ObjectType: auditSession, ObjectID: "5", Limit: defaultListLimit + 1}},
		{name: "one object", query: "?type=vehicle&id=bus-1", want: auditFilter{ObjectType: auditVehicle, ObjectID: "bus-1", Limit: defaultListLimit + 1}},
		{name: "one admin", query: "?actor=7", want: auditFilter{ActorID: &actor, Limit: defaultListLimit + 1}},
		{name: "a page", query: "?limit=20&offset=40", want: auditFilter{Limit: 21, Offset: 40}},
		{name: "the largest page", query: "?limit=200", want: auditFilter{Limit: maxListLimit + 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &fakeAuditLog{}
			w := getAuditAPI(t, log, roleAdmin, tt.query)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Len(t, log.asked, 1)
			assert.Equal(t, tt.want, log.asked[0])
		})
	}
}

// TestListAuditEntries_BadQuery: a filter the page would refuse, or a limit
// or offset the other admin lists would, is a 400 with the reason, and the
// log is not read.
func TestListAuditEntries_BadQuery(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{"?type=bus", "type must be vehicle, user, trip, api_key or session"},
		{"?id=bus-1", "id needs a type"},
		{"?actor=ops", "actor must be a user id"},
		{"?actor=0", "actor must be a user id"},
		{"?limit=0", "limit must be between 1 and 200"},
		{"?limit=201", "limit must be between 1 and 200"},
		{"?offset=-1", "offset must be between 0 and 2147483647"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			log := &fakeAuditLog{}
			w := getAuditAPI(t, log, roleAdmin, tt.query)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.JSONEq(t, `{"error":"`+tt.want+`"}`, w.Body.String())
			assert.Empty(t, log.asked)
		})
	}
}

// TestListAuditEntries_HasMore: the extra row the store returns past limit
// sets has_more and is not sent.
func TestListAuditEntries_HasMore(t *testing.T) {
	entries := auditAPIEntries()

	w := getAuditAPI(t, &fakeAuditLog{entries: entries}, roleAdmin, "?limit=1")
	require.Equal(t, http.StatusOK, w.Code)
	var page auditListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	assert.True(t, page.HasMore)
	assert.Equal(t, 1, page.Count)
	require.Len(t, page.Entries, 1)
	assert.Equal(t, int64(12), page.Entries[0].ID, "the newest row, as the store returned it first")

	w = getAuditAPI(t, &fakeAuditLog{entries: entries}, roleAdmin, "?limit=2")
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	assert.False(t, page.HasMore, "a page that fits exactly has no more")
	assert.Equal(t, 2, page.Count)
}

// TestListAuditEntries_EmptyLog: no rows is an empty list, not null.
func TestListAuditEntries_EmptyLog(t *testing.T) {
	w := getAuditAPI(t, &fakeAuditLog{}, roleAdmin, "")

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"count":0,"has_more":false,"entries":[]}`, w.Body.String())
}

type failingAuditLog struct{}

func (failingAuditLog) ListAuditEntries(context.Context, auditFilter) ([]auditEntry, error) {
	return nil, errors.New("connection refused")
}

func TestListAuditEntries_StoreError(t *testing.T) {
	w := getAuditAPI(t, failingAuditLog{}, roleAdmin, "")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.JSONEq(t, `{"error":"internal server error"}`, w.Body.String())
	assert.NotContains(t, w.Body.String(), "connection refused")
}

// TestAuditAPI_Gates sends GET /api/v1/admin/audit through the whole server:
// no token is a 401, a driver's a 403, and an admin's and a viewer's are
// answered, the address in the admin's only.
func TestAuditAPI_Gates(t *testing.T) {
	h := newViewerTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/audit", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	driver := serveAs(t, h, roleDriver, http.MethodGet, "/api/v1/admin/audit")
	assert.Equal(t, http.StatusForbidden, driver.Code)
	assert.Equal(t, "admin access required", decodeError(t, driver))

	admin := serveAs(t, h, roleAdmin, http.MethodGet, "/api/v1/admin/audit")
	require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
	assert.Contains(t, admin.Body.String(), `"actor_ip":"`+viewerPagesIP+`"`)

	viewer := serveAs(t, h, roleViewer, http.MethodGet, "/api/v1/admin/audit")
	require.Equal(t, http.StatusOK, viewer.Code, viewer.Body.String())
	assert.Contains(t, viewer.Body.String(), `"actor_email":"ops@example.org"`)
	assert.NotContains(t, viewer.Body.String(), "actor_ip")
	assert.NotContains(t, viewer.Body.String(), viewerPagesIP)
}

// TestAuditAPI_ThroughTheServer is the API against the database: an admin's
// change made through the API is read back through it, with the address for
// the admin and without it for a viewer, and an admin who made no change has
// an empty list.
func TestAuditAPI_ThroughTheServer(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := context.Background()
	admin, err := store.CreateUser(ctx, "Ops", "aud-api-ops@example.org", "a-password", roleAdmin)
	require.NoError(t, err)
	viewer, err := store.CreateUser(ctx, "Watcher", "aud-api-viewer@example.org", "a-password", roleViewer)
	require.NoError(t, err)
	token := func(u *UserResponse) string {
		t.Helper()
		tok, err := generateJWT(&User{ID: u.ID, Email: u.Email, Role: u.Role}, testSecret, defaultAccessTokenTTL)
		require.NoError(t, err)
		return tok
	}

	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return nil }, testReportSettings)
	h, err := newHandler(store, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, trustProxy: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, nil, nil,
		newAlertWatcher(store, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)
	send := func(method, path, body, tok string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-Forwarded-For", "198.51.100.31")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	w := send(http.MethodPost, "/api/v1/admin/vehicles", `{"id":"aud-api-read","label":"Read back"}`, token(admin))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	read := func(tok, query string) auditListResponse {
		t.Helper()
		w := send(http.MethodGet, "/api/v1/admin/audit"+query, "", tok)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var page auditListResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
		return page
	}

	page := read(token(admin), "?type=vehicle&id=aud-api-read")
	require.Equal(t, 1, page.Count)
	e := page.Entries[0]
	require.NotNil(t, e.ActorID)
	assert.Equal(t, admin.ID, *e.ActorID)
	assert.Equal(t, "aud-api-ops@example.org", e.ActorEmail)
	require.NotNil(t, e.ActorIP)
	assert.Equal(t, "198.51.100.31", *e.ActorIP)
	assert.Equal(t, auditCreate, e.Action)
	assert.Equal(t, "null", string(e.Before), "a create has no before")
	assert.JSONEq(t, `{"label":"Read back","agency_tag":"","active":true}`, string(e.After))
	assert.Equal(t, time.UTC, e.At.Location())
	assert.WithinDuration(t, time.Now(), e.At, time.Minute)

	asViewer := read(token(viewer), "?type=vehicle&id=aud-api-read")
	require.Equal(t, 1, asViewer.Count)
	assert.Nil(t, asViewer.Entries[0].ActorIP)
	assert.Equal(t, e.ID, asViewer.Entries[0].ID)

	w = send(http.MethodGet, "/api/v1/admin/audit?actor="+idText(viewer.ID), "", token(admin))
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"count":0,"has_more":false,"entries":[]}`, w.Body.String(), "a viewer made no change")
}
