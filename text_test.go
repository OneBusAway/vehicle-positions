package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
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

func TestStorableText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", true},
		{"ASCII", "Route 12", true},
		{"Devanagari", "बस", true},
		{"a replacement character, which is valid", "\uFFFD", true},
		{"a tab and a newline", "a\tb\n", true},
		{"a NUL", "a\x00b", false},
		{"a byte that starts no character", "\xff", false},
		{"a character cut short", "\xe0\xa4", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, storableText(tt.text))
		})
	}
}

func TestRefuseUnstorableURL(t *testing.T) {
	tests := []struct {
		target string
		want   int
	}{
		{"/api/v1/admin/vehicles", http.StatusNoContent},
		{"/api/v1/admin/vehicles?q=%E0%A4%AC&agency_tag=metro", http.StatusNoContent},
		{"/api/v1/admin/vehicles?q=%00", http.StatusBadRequest},
		{"/api/v1/admin/vehicles?q=ok&q=%ff", http.StatusBadRequest},
		{"/admin/vehicles?q=%ff", http.StatusBadRequest},
		{"/admin/vehicles?q=%E0%A4%AC", http.StatusNoContent},
		{"/admin/vehicles/%00/edit", http.StatusNotFound},
		{"/admin/vehicles/%ff/edit", http.StatusNotFound},
		{"/admin/vehicles/%E0%A4%AC/edit", http.StatusNoContent},
		// The API's own check of an id answers 400, so it is left to it.
		{"/api/v1/admin/vehicles/%00", http.StatusNoContent},
		// Outside the admin API and pages, no query value goes to the database.
		{"/gtfs-rt/vehicle-positions?format=%ff", http.StatusNoContent},
		{"/api/v1/rider/trips/T1/status?x=%ff", http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			h := refuseUnstorableURL(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.target, nil))

			assert.Equal(t, tt.want, w.Code)
			if tt.want == http.StatusBadRequest {
				assert.Contains(t, w.Body.String(), "query parameters must be UTF-8 with no NUL characters")
				assert.Equal(t, strings.HasPrefix(tt.target, "/api/"), strings.HasPrefix(w.Header().Get("Content-Type"), "application/json"))
			}
		})
	}
}

// textServer is the whole server on a real database, with an admin's and a
// driver's sessions, for sending it text Postgres can't hold. logs is what it
// logs from then on.
type textServer struct {
	http.Handler
	store         *Store
	admin, driver string
	driverID      int64
	logs          *bytes.Buffer
}

// newTextServer is not safe for t.Parallel(); it captures the global logger.
func newTextServer(t *testing.T) *textServer {
	t.Helper()
	store := newAuditTestStore(t)
	ctx := context.Background()
	session := func(name, email, role string) (string, int64) {
		user, err := store.CreateUser(ctx, name, email, "a-password", role)
		require.NoError(t, err)
		token, err := generateJWT(&User{ID: user.ID, Email: user.Email, Role: role}, testSecret, sessionLifetime)
		require.NoError(t, err)
		return token, user.ID
	}
	s := &textServer{store: store}
	s.admin, _ = session("Ops", "aud-ops@example.org", roleAdmin)
	s.driver, s.driverID = session("Ana", "aud-ana@example.org", roleDriver)
	_, err := store.CreateVehicle(ctx, "aud-v", "Label", "")
	require.NoError(t, err)
	_, err = store.CreateAssignment(asTestAdmin(), s.driverID, "aud-v")
	require.NoError(t, err)

	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	reportLimiter := NewVehicleRateLimiter()
	t.Cleanup(reportLimiter.Stop)
	newLimiter := func() *LoginRateLimiter {
		l := NewLoginRateLimiter()
		t.Cleanup(l.Stop)
		return l
	}
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return nil }, testReportSettings)
	s.Handler, err = newHandler(serverStore{store, &noopStore{}}, tracker, reportLimiter, newLimiter(), newLimiter(), nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, nil, nil,
		newAlertWatcher(store, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)
	s.logs = captureLogs(t)
	return s
}

// send sends a request with session as its bearer token and its cookie.
func (s *textServer) send(method, target, contentType, body, session string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if session != "" {
		req.Header.Set("Authorization", "Bearer "+session)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

const (
	jsonType = "application/json"
	formType = "application/x-www-form-urlencoded"
)

// TestServer_TextPostgresCannotHold: text Postgres can't hold is the caller's
// mistake, through the whole server and a real database: a 400 in a search or a
// filter, and a wrong password at the sign-ins, which anyone can reach, where it
// is an email no account has. Nothing is logged as an error.
func TestServer_TextPostgresCannotHold(t *testing.T) {
	s := newTextServer(t)

	for _, path := range []string{
		"/api/v1/admin/vehicles?q=", "/api/v1/admin/vehicles?agency_tag=",
		"/api/v1/admin/users?q=", "/api/v1/admin/trips?q=", "/api/v1/admin/trips?vehicle_id=",
		"/api/v1/admin/audit?type=vehicle&id=",
		"/admin/vehicles?q=", "/admin/vehicles?agency_tag=", "/admin/users?q=", "/admin/trips?q=", "/admin/audit?type=vehicle&id=",
	} {
		for _, text := range []string{"%00", "%ff"} {
			w := s.send(http.MethodGet, path+text, "", "", s.admin)
			assert.Equal(t, http.StatusBadRequest, w.Code, "%s%s: %s", path, text, w.Body.String())
		}
	}

	body, err := json.Marshal(LoginRequest{Email: "nobody\x00@example.org", Password: "a password"})
	require.NoError(t, err)
	w := s.send(http.MethodPost, "/api/v1/auth/login", jsonType, string(body), "")
	assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	assert.Equal(t, "invalid email or password", decodeError(t, w))

	for _, email := range []string{"nobody\x00@example.org", "nobody\xff@example.org"} {
		w := s.send(http.MethodPost, "/admin/login", formType, url.Values{"email": {email}, "password": {"a password"}}.Encode(), "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "Invalid email or password.")
	}
	assert.NotContains(t, s.logs.String(), "level=ERROR")
}

// TestServer_BodiesPostgresCannotHold: the same in what a request saves. A JSON
// body is refused with 400, and a form is shown again with 422, each naming the
// field. JSON has only the NUL, as a decoder has already replaced a byte that
// isn't UTF-8.
func TestServer_BodiesPostgresCannotHold(t *testing.T) {
	s := newTextServer(t)
	const cannotBeSaved = " has a character that can't be saved"

	jsonBodies := []struct {
		method, path, session string
		body                  map[string]any
		want                  string
	}{
		{"POST", "/api/v1/admin/vehicles", s.admin, map[string]any{"id": "aud-x", "label": "a\x00"}, "label" + cannotBeSaved},
		{"POST", "/api/v1/admin/vehicles", s.admin, map[string]any{"id": "aud-x", "agency_tag": "a\x00"}, "agency_tag" + cannotBeSaved},
		{"POST", "/api/v1/admin/users", s.admin, map[string]any{"name": "a\x00", "email": "aud-x@example.org", "password": "a-password", "role": "driver"}, "name" + cannotBeSaved},
		{"POST", "/api/v1/admin/users", s.admin, map[string]any{"name": "X", "email": "\"a\x00\"@example.org", "password": "a-password", "role": "driver"}, "email is not valid"},
		{"PUT", fmt.Sprintf("/api/v1/admin/users/%d", s.driverID), s.admin, map[string]any{"name": "a\x00", "email": "aud-ana@example.org", "role": "driver"}, "name" + cannotBeSaved},
		{"POST", "/api/v1/admin/api-keys", s.admin, map[string]any{"name": "a\x00"}, "name" + cannotBeSaved},
		{"POST", "/api/v1/trips/start", s.driver, map[string]any{"vehicle_id": "aud-v", "route_id": "a\x00"}, "route_id" + cannotBeSaved},
		{"POST", "/api/v1/trips/start", s.driver, map[string]any{"vehicle_id": "aud-v", "gtfs_trip_id": "a\x00"}, "gtfs_trip_id" + cannotBeSaved},
		{"POST", "/api/v1/locations", s.driver, map[string]any{"vehicle_id": "aud-v", "latitude": 1, "longitude": 2, "timestamp": time.Now().Unix(), "trip_id": "a\x00"}, "trip_id" + cannotBeSaved},
		{"POST", "/api/v1/locations", s.driver, map[string]any{"vehicle_id": "aud-v", "latitude": 1, "longitude": 2, "timestamp": time.Now().Unix(), "route_id": "a\x00"}, "route_id" + cannotBeSaved},
	}
	for _, tt := range jsonBodies {
		body, err := json.Marshal(tt.body)
		require.NoError(t, err)
		w := s.send(tt.method, tt.path, jsonType, string(body), tt.session)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%s %s: %s", tt.path, body, w.Body.String())
		assert.Equal(t, tt.want, decodeError(t, w), "%s %s", tt.path, body)
	}

	user := fmt.Sprintf("/admin/users/%d", s.driverID)
	for _, text := range []string{"a\x00", "a\xff"} {
		forms := []struct {
			path string
			form url.Values
			code int
			want string
		}{
			{"/admin/vehicles", url.Values{"id": {"aud-x"}, "label": {text}}, http.StatusUnprocessableEntity, "label" + cannotBeSaved},
			{"/admin/vehicles", url.Values{"id": {"aud-x"}, "agency_tag": {text}}, http.StatusUnprocessableEntity, "agency_tag" + cannotBeSaved},
			{"/admin/vehicles/aud-v", url.Values{"label": {text}}, http.StatusUnprocessableEntity, "label" + cannotBeSaved},
			{"/admin/users", url.Values{"name": {text}, "email": {"aud-x@example.org"}, "password": {"a-password"}, "role": {"driver"}}, http.StatusUnprocessableEntity, "name" + cannotBeSaved},
			{"/admin/users", url.Values{"name": {"X"}, "email": {`"` + text + `"@example.org`}, "password": {"a-password"}, "role": {"driver"}}, http.StatusUnprocessableEntity, "email is not valid"},
			{user, url.Values{"name": {text}, "email": {"aud-ana@example.org"}, "role": {"driver"}}, http.StatusUnprocessableEntity, "name" + cannotBeSaved},
			{"/admin/api-keys", url.Values{"name": {text}}, http.StatusUnprocessableEntity, "name" + cannotBeSaved},
			{user + "/vehicles", url.Values{"vehicle_id": {text}}, http.StatusNotFound, ""},
		}
		for _, tt := range forms {
			w := s.send(http.MethodPost, tt.path, formType, tt.form.Encode(), s.admin)
			assert.Equal(t, tt.code, w.Code, "%s %s", tt.path, tt.form.Encode())
			assert.Contains(t, w.Body.String(), html.EscapeString(tt.want), "%s %s", tt.path, tt.form.Encode())
		}

		id := url.PathEscape(text)
		for _, page := range [][2]string{
			{"GET", "/admin/vehicles/" + id + "/edit"}, {"POST", "/admin/vehicles/" + id},
			{"POST", "/admin/vehicles/" + id + "/deactivate"}, {"POST", "/admin/vehicles/" + id + "/activate"},
			{"POST", user + "/vehicles/" + id + "/remove"},
		} {
			w := s.send(page[0], page[1], formType, "", s.admin)
			assert.Equal(t, http.StatusNotFound, w.Code, "%s %s", page[0], page[1])
		}
	}

	// A CSV row says so in the preview, and the rest of the file is planned.
	for _, tt := range []struct{ kind, file, want string }{
		{"drivers", "email,name,vehicle_ids\naud-new@example.org,N,\naud-x@example.org,a\x00,\n", "name" + cannotBeSaved},
		{"drivers", "email,name,vehicle_ids\na\x00@example.org,X,\n", "email is not valid"},
		{"vehicles", "id,label,agency_tag\naud-new,N,\naud-x,a\x00,\n", "label" + cannotBeSaved},
		{"vehicles", "id,label,agency_tag\naud-x,,a\x00\n", "agency_tag" + cannotBeSaved},
	} {
		contentType, body := multipartFile(t, []byte(tt.file))
		file, err := io.ReadAll(body)
		require.NoError(t, err)
		w := s.send(http.MethodPost, "/admin/import/"+tt.kind, contentType, string(file), s.admin)
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code, "%q", tt.file)
		assert.Contains(t, w.Body.String(), html.EscapeString(tt.want), "%q", tt.file)
	}
	assert.NotContains(t, s.logs.String(), "level=ERROR")
}
