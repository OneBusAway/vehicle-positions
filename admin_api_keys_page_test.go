package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawAPIKeyPattern matches a raw key as generateAPIKey makes it: 32 random
// bytes, hex-encoded.
var rawAPIKeyPattern = regexp.MustCompile(`[0-9a-f]{64}`)

// newAPIKeysMux serves the admin UI over the given key store.
func newAPIKeysMux(t *testing.T, keys APIKeyManager) *http.ServeMux {
	t.Helper()
	ui := newTestAdminUI(t)
	ui.apiKeys = keys
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	return mux
}

// postAdminForm submits a form body to path as a signed-in admin.
func postAdminForm(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// captureLogs points the default logger at a buffer for the rest of the test.
// Not safe for t.Parallel(); uses global logger.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	return &logs
}

// seedAPIKeys builds n active keys with zero-padded names so a name like
// "Consumer 050" can't be a substring of another row's name.
func seedAPIKeys(n int) []APIKey {
	keys := make([]APIKey, 0, n)
	for i := 1; i <= n; i++ {
		keys = append(keys, APIKey{ID: int64(i), Name: fmt.Sprintf("Consumer %03d", i), Active: true})
	}
	return keys
}

// TestAPIKeysPage_ListsKeys verifies each row's name, created time, last-used
// time and status, that only an active key can be deactivated, and that the
// stored hash never reaches the page.
func TestAPIKeysPage_ListsKeys(t *testing.T) {
	lastUsed := time.Date(2026, 11, 12, 18, 5, 0, 0, time.UTC)
	mux := newAPIKeysMux(t, &fakeAPIKeyManager{keys: []APIKey{
		{ID: 1, Name: "OneBusAway", KeyHash: "hash-of-the-first-key", Active: true, CreatedAt: time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC), LastUsedAt: &lastUsed},
		{ID: 2, Name: "Old trip planner", KeyHash: "hash-of-the-second-key", Active: false, CreatedAt: time.Date(2026, 10, 2, 7, 0, 0, 0, time.UTC)},
	}})

	body := getAdminPage(t, mux, "/admin/api-keys")

	assert.Contains(t, body, "2 keys on this page.")
	assert.Contains(t, body, "OneBusAway")
	assert.Contains(t, body, "2026-11-01 09:30 UTC")
	assert.Contains(t, body, "2026-11-12 18:05 UTC")
	assert.Contains(t, body, "Old trip planner")
	assert.Contains(t, body, "2026-10-02 07:00 UTC")
	assert.Equal(t, 1, strings.Count(body, ">never<"), "only the key that was never used says so")
	assert.Equal(t, 1, strings.Count(body, "Deactivated"))

	assert.Contains(t, body, `action="/admin/api-keys/1/deactivate"`)
	assert.NotContains(t, body, `action="/admin/api-keys/2/deactivate"`, "a deactivated key has nothing left to deactivate")
	assert.NotContains(t, body, "/activate\"", "the API has no reactivate, so the page must not offer one")

	assert.NotContains(t, body, "hash-of-the")
	assert.NotContains(t, body, "cannot be shown again", "no key was made on this request")
}

func TestAPIKeysPage_EmptyState(t *testing.T) {
	mux := newAPIKeysMux(t, &fakeAPIKeyManager{})

	body := getAdminPage(t, mux, "/admin/api-keys")

	assert.Contains(t, body, "No feed API keys yet.")
	assert.Contains(t, body, `action="/admin/api-keys"`, "the create form is there before the first key")
	assert.NotContains(t, body, "/deactivate")
	assert.NotContains(t, body, "Page 1", "one empty page has no pagination")
}

// TestAPIKeysPage_Paginates verifies the list shows at most one page of keys,
// that ?page=2 shows the next slice with no row repeated or skipped, and that
// prev/next links appear only where there's a page to go to.
func TestAPIKeysPage_Paginates(t *testing.T) {
	mux := newAPIKeysMux(t, &fakeAPIKeyManager{keys: seedAPIKeys(adminPageSize + 5)})

	first := getAdminPage(t, mux, "/admin/api-keys")
	assert.Equal(t, adminPageSize, strings.Count(first, "/deactivate\""), "page 1 must hold exactly one page of rows")
	assert.Contains(t, first, "Consumer 001")
	assert.Contains(t, first, fmt.Sprintf("Consumer %03d", adminPageSize))
	assert.NotContains(t, first, fmt.Sprintf("Consumer %03d", adminPageSize+1), "the next page's first row must not leak onto page 1")
	assert.Contains(t, first, `href="/admin/api-keys?page=2"`)
	assert.NotContains(t, first, "Previous", "page 1 has no previous page")

	second := getAdminPage(t, mux, "/admin/api-keys?page=2")
	assert.Equal(t, 5, strings.Count(second, "/deactivate\""), "the last page holds the remaining rows")
	assert.Contains(t, second, fmt.Sprintf("Consumer %03d", adminPageSize+1))
	assert.NotContains(t, second, fmt.Sprintf("Consumer %03d", adminPageSize), "page 1's last row must not repeat on page 2")
	assert.Contains(t, second, `href="/admin/api-keys?page=1"`)
	assert.NotContains(t, second, "Next", "the last page has no next page")
}

// TestAPIKeysPage_ExactlyOnePageHasNoNext pins the boundary: a full page with
// nothing after it must not offer a next page.
func TestAPIKeysPage_ExactlyOnePageHasNoNext(t *testing.T) {
	mux := newAPIKeysMux(t, &fakeAPIKeyManager{keys: seedAPIKeys(adminPageSize)})

	body := getAdminPage(t, mux, "/admin/api-keys")

	assert.Equal(t, adminPageSize, strings.Count(body, "/deactivate\""))
	assert.NotContains(t, body, "Next")
}

// TestAPIKeysPage_PageOutOfRange verifies a page number that is valid but
// past the last key renders an empty page with a way back, as the other
// admin lists do, and one that isn't a page number renders page 1.
func TestAPIKeysPage_PageOutOfRange(t *testing.T) {
	mux := newAPIKeysMux(t, &fakeAPIKeyManager{keys: seedAPIKeys(adminPageSize + 5)})

	past := getAdminPage(t, mux, "/admin/api-keys?page=9")
	assert.Contains(t, past, "No feed API keys yet.")
	assert.Contains(t, past, `href="/admin/api-keys?page=8"`)
	assert.NotContains(t, past, "Next")

	for _, page := range []string{"abc", "0", "-3", "1000001", ""} {
		t.Run("page="+page, func(t *testing.T) {
			body := getAdminPage(t, mux, "/admin/api-keys?page="+page)
			assert.Contains(t, body, "Consumer 001", "an invalid page must render page 1")
			assert.Contains(t, body, "Page 1")
		})
	}
}

func TestAPIKeysPage_StoreError(t *testing.T) {
	mux := newAPIKeysMux(t, &fakeAPIKeyManager{listErr: errors.New("connection refused")})

	w := requestAdminPage(t, mux, "/admin/api-keys")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "internal server error")
	assert.NotContains(t, w.Body.String(), "connection refused")
}

// TestAPIKeysPage_ShowsDeactivatedFlash verifies the code apiKeyDeactivate
// flashes resolves to a message on the page it redirects to.
func TestAPIKeysPage_ShowsDeactivatedFlash(t *testing.T) {
	mux := newAPIKeysMux(t, &fakeAPIKeyManager{})

	req := httptest.NewRequest(http.MethodGet, "/admin/api-keys", nil)
	req.AddCookie(cookieFor(t, "admin"))
	req.AddCookie(&http.Cookie{Name: flashCookieName, Value: "api_key_deactivated"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "API key deactivated.")
}

// TestAdminSidebar_LinksToAPIKeys verifies the sidebar item is on every admin
// page and is marked as the current one only on its own page.
func TestAdminSidebar_LinksToAPIKeys(t *testing.T) {
	mux := newAPIKeysMux(t, &fakeAPIKeyManager{})
	link := regexp.MustCompile(`href="/admin/api-keys"\s+data-active="(true|false)"`)

	for path, wantActive := range map[string]string{
		"/admin/api-keys":  "true",
		"/admin/dashboard": "false",
		"/admin/users":     "false",
	} {
		t.Run(path, func(t *testing.T) {
			body := getAdminPage(t, mux, path)
			match := link.FindStringSubmatch(body)
			require.NotNil(t, match, "the sidebar must link to the Feed API keys page")
			assert.Equal(t, wantActive, match[1])
			assert.Contains(t, body, "Feed API keys")
		})
	}
}

// TestAPIKeyCreate_ShowsKeyOnce is the page's main promise: the response to
// the POST carries the raw key and says it cannot be shown again, the store
// holds only its hash, and the key appears nowhere else: not in a redirect,
// a cookie or a log, and not on the list page afterwards.
// Not safe for t.Parallel(); uses global logger.
func TestAPIKeyCreate_ShowsKeyOnce(t *testing.T) {
	store := &fakeAPIKeyManager{}
	mux := newAPIKeysMux(t, store)
	logs := captureLogs(t)

	w := postAdminForm(t, mux, "/admin/api-keys", url.Values{"name": {"OneBusAway"}}.Encode())

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	found := rawAPIKeyPattern.FindAllString(body, -1)
	require.Len(t, found, 1, "the page must show the new key, and show it in one place")
	rawKey := found[0]

	require.Len(t, store.keys, 1)
	assert.Equal(t, "OneBusAway", store.keys[0].Name)
	assert.Equal(t, hashAPIKey(rawKey), store.keys[0].KeyHash, "the key on the page must be the one whose hash was stored")
	assert.NotContains(t, body, store.keys[0].KeyHash)

	assert.Contains(t, body, "1 key on this page.")
	assert.Contains(t, body, "Key for OneBusAway created.")
	assert.Contains(t, body, "It cannot be shown again.")
	assert.Contains(t, body, `action="/admin/api-keys/1/deactivate"`, "the new key must be in the list on the same page")

	assert.Empty(t, w.Header().Get("Location"), "a redirect would have to carry the key in a URL or a cookie")
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"), "the browser must not keep a copy of the page")
	for _, c := range w.Result().Cookies() {
		assert.NotContains(t, c.Value, rawKey, "cookie %s", c.Name)
	}

	assert.Contains(t, logs.String(), `msg="api key created"`)
	assert.Contains(t, logs.String(), "api_key_id=1")
	assert.NotContains(t, logs.String(), rawKey, "the raw key must never be logged")

	after := getAdminPage(t, mux, "/admin/api-keys")
	assert.Contains(t, after, "OneBusAway")
	assert.Empty(t, rawAPIKeyPattern.FindString(after), "the key must not be shown a second time")
	assert.NotContains(t, after, "cannot be shown again")
}

// TestAPIKeyCreate_NewKeyLeadsTheList verifies the new key's row is on the
// page that shows the key even when the list was already a full page, with
// the row it pushed off reachable on the next one.
func TestAPIKeyCreate_NewKeyLeadsTheList(t *testing.T) {
	store := &fakeAPIKeyManager{keys: seedAPIKeys(adminPageSize)}
	mux := newAPIKeysMux(t, store)

	w := postAdminForm(t, mux, "/admin/api-keys", url.Values{"name": {"Newest consumer"}}.Encode())

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, ">Newest consumer<")
	assert.Equal(t, adminPageSize, strings.Count(body, "/deactivate\""), "the page still holds one page of rows")
	assert.NotContains(t, body, fmt.Sprintf("Consumer %03d", adminPageSize), "the last row moves to the next page")
	assert.Contains(t, body, `href="/admin/api-keys?page=2"`)
}

// TestAPIKeyCreate_Validation verifies the form refuses what the JSON API
// refuses, with the API's own messages, and that a refused name makes no key.
func TestAPIKeyCreate_Validation(t *testing.T) {
	// Not hex digits, so the name can't be mistaken for a raw key below.
	tooLong := strings.Repeat("n", maxFieldLength+1)

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantError  string
		wantValue  string
	}{
		{"empty name", "name=", http.StatusUnprocessableEntity, "name is required", ""},
		{"no name field", "", http.StatusUnprocessableEntity, "name is required", ""},
		{"name past the length limit", "name=" + tooLong, http.StatusUnprocessableEntity, "name must be at most 255 characters", tooLong},
		{"malformed form", "name=%zz", http.StatusBadRequest, "Invalid form submission.", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAPIKeyManager{keys: []APIKey{{ID: 1, Name: "Existing consumer", Active: true}}}
			mux := newAPIKeysMux(t, store)

			w := postAdminForm(t, mux, "/admin/api-keys", tc.body)

			assert.Equal(t, tc.wantStatus, w.Code)
			body := w.Body.String()
			assert.Contains(t, body, `role="alert">`+tc.wantError+`<`)
			assert.Contains(t, body, `value="`+tc.wantValue+`"`, "the submitted name must come back in the form")
			assert.Contains(t, body, "Existing consumer", "the list must still render under the error")
			assert.Len(t, store.keys, 1, "a refused name must not make a key")
			assert.Empty(t, rawAPIKeyPattern.FindString(body))
			assert.NotContains(t, body, "cannot be shown again")
		})
	}
}

// TestAPIKeyCreate_NameAtTheLengthLimit is the accepted side of the boundary
// in TestAPIKeyCreate_Validation.
func TestAPIKeyCreate_NameAtTheLengthLimit(t *testing.T) {
	store := &fakeAPIKeyManager{}
	mux := newAPIKeysMux(t, store)

	w := postAdminForm(t, mux, "/admin/api-keys", "name="+strings.Repeat("a", maxFieldLength))

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, store.keys, 1)
	assert.Len(t, store.keys[0].Name, maxFieldLength)
}

func TestAPIKeyCreate_StoreError(t *testing.T) {
	store := &fakeAPIKeyManager{createErr: errors.New("connection refused")}
	mux := newAPIKeysMux(t, store)

	w := postAdminForm(t, mux, "/admin/api-keys", "name=OneBusAway")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "internal server error")
	assert.NotContains(t, w.Body.String(), "connection refused")
	assert.Empty(t, rawAPIKeyPattern.FindString(w.Body.String()), "a key that was not stored must not be shown")
}

// TestAPIKeyCreate_ListErrorMakesNoKey pins the order in apiKeyCreate: the
// list is read before the key is made, so a failed read cannot leave behind a
// key whose only copy was on a page that never rendered.
func TestAPIKeyCreate_ListErrorMakesNoKey(t *testing.T) {
	store := &fakeAPIKeyManager{listErr: errors.New("connection refused")}
	mux := newAPIKeysMux(t, store)

	w := postAdminForm(t, mux, "/admin/api-keys", "name=OneBusAway")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "connection refused")
	assert.Empty(t, store.keys, "no key may be made when the page that shows it cannot be built")
}

// TestAPIKeyDeactivate verifies the success path deactivates the key in the
// URL, flashes, redirects back to the list, and logs the admin who did it.
// Not safe for t.Parallel(); uses global logger.
func TestAPIKeyDeactivate(t *testing.T) {
	store := &fakeAPIKeyManager{}
	mux := newAPIKeysMux(t, store)
	logs := captureLogs(t)

	w := postAdminForm(t, mux, "/admin/api-keys/3/deactivate", "")

	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/api-keys", w.Header().Get("Location"))
	assert.Equal(t, "api_key_deactivated", flashCodeSet(w))
	assert.Equal(t, []int64{3}, store.deactivated)
	assert.Contains(t, logs.String(), `msg="api key revoked"`)
	assert.Contains(t, logs.String(), "api_key_id=3")
	assert.Contains(t, logs.String(), "admin_sub=9")
}

func TestAPIKeyDeactivate_NotFound(t *testing.T) {
	store := &fakeAPIKeyManager{deactivateErr: ErrAPIKeyNotFound}
	mux := newAPIKeysMux(t, store)

	w := postAdminForm(t, mux, "/admin/api-keys/999/deactivate", "")

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Empty(t, flashCodeSet(w))
}

func TestAPIKeyDeactivate_InvalidID(t *testing.T) {
	// The last one overflows int64: ParseInt returns its max along with the
	// error, so only the error check keeps it from the store.
	for _, id := range []string{"abc", "0", "-1", "99999999999999999999"} {
		t.Run(id, func(t *testing.T) {
			store := &fakeAPIKeyManager{}
			mux := newAPIKeysMux(t, store)

			w := postAdminForm(t, mux, "/admin/api-keys/"+id+"/deactivate", "")

			assert.Equal(t, http.StatusNotFound, w.Code)
			assert.Empty(t, store.deactivated, "the store must not be asked to deactivate key %q", id)
		})
	}
}

func TestAPIKeyDeactivate_StoreError(t *testing.T) {
	store := &fakeAPIKeyManager{deactivateErr: errors.New("connection refused")}
	mux := newAPIKeysMux(t, store)

	w := postAdminForm(t, mux, "/admin/api-keys/3/deactivate", "")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "internal server error")
	assert.NotContains(t, w.Body.String(), "connection refused")
	assert.Empty(t, flashCodeSet(w), "a failed deactivation must not reach the flash")
}

// apiKeyCountingStore is a noopStore that counts the key writes that reach
// it, for the tests that drive the whole production handler.
type apiKeyCountingStore struct {
	noopStore
	created     int
	deactivated int
}

func (s *apiKeyCountingStore) CreateAPIKey(_ context.Context, name, keyHash string) (*APIKey, error) {
	s.created++
	return &APIKey{ID: 1, Name: name, KeyHash: keyHash, Active: true}, nil
}

func (s *apiKeyCountingStore) DeactivateAPIKey(_ context.Context, _ int64) error {
	s.deactivated++
	return nil
}

// newAPIKeysHandler builds the production handler, CSRF protection included,
// over a store that counts key writes.
func newAPIKeysHandler(t *testing.T) (http.Handler, *apiKeyCountingStore) {
	t.Helper()
	store := &apiKeyCountingStore{}
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	h, err := newHandler(store, tracker, nil, ll, ll, nil, testSecret, testTTLs, time.Now(), adminUIConfig{enabled: true, stalenessThreshold: 5 * time.Minute}, false, nil, nil, nil)
	require.NoError(t, err)
	return h, store
}

// TestAPIKeyRoutes_CrossOriginPostRefused verifies both writes sit behind the
// handler's CSRF protection: a signed-in admin's browser posting from another
// site is refused before the store is reached, and the same request from the
// admin UI itself goes through, so the refusal is the protection's and not
// some other failure.
func TestAPIKeyRoutes_CrossOriginPostRefused(t *testing.T) {
	routes := []struct {
		path       string
		body       string
		wantStatus int
	}{
		{"/admin/api-keys", "name=OneBusAway", http.StatusOK},
		{"/admin/api-keys/1/deactivate", "", http.StatusSeeOther},
	}
	crossOrigin := []struct {
		name   string
		header string
		value  string
	}{
		{"cross-site fetch metadata", "Sec-Fetch-Site", "cross-site"},
		{"foreign origin", "Origin", "https://evil.example"},
	}

	for _, route := range routes {
		for _, tc := range crossOrigin {
			t.Run(tc.name+" "+route.path, func(t *testing.T) {
				h, store := newAPIKeysHandler(t)

				req := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(route.body))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set(tc.header, tc.value)
				req.AddCookie(cookieFor(t, "admin"))
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)

				assert.Equal(t, http.StatusForbidden, w.Code)
				assert.Zero(t, store.created+store.deactivated, "a refused request must not reach the store")
				assert.Empty(t, rawAPIKeyPattern.FindString(w.Body.String()))
			})
		}

		t.Run("same origin "+route.path, func(t *testing.T) {
			h, store := newAPIKeysHandler(t)

			req := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(route.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.AddCookie(cookieFor(t, "admin"))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, route.wantStatus, w.Code)
			assert.Equal(t, 1, store.created+store.deactivated)
		})
	}
}

// TestAPIKeyRoutes_SignedOutGoToLogin verifies none of the three routes does
// anything without an admin session: no session and a driver's session are
// both sent to the sign-in page, and neither write reaches the store.
func TestAPIKeyRoutes_SignedOutGoToLogin(t *testing.T) {
	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/admin/api-keys", ""},
		{http.MethodPost, "/admin/api-keys", "name=OneBusAway"},
		{http.MethodPost, "/admin/api-keys/1/deactivate", ""},
	}

	for _, route := range routes {
		for _, session := range []string{"none", "driver"} {
			t.Run(session+" "+route.method+" "+route.path, func(t *testing.T) {
				h, store := newAPIKeysHandler(t)

				req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if session != "none" {
					req.AddCookie(cookieFor(t, session))
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)

				assert.Equal(t, http.StatusSeeOther, w.Code)
				assert.Equal(t, "/admin/login", w.Header().Get("Location"))
				assert.Zero(t, store.created+store.deactivated, "a request without an admin session must not reach the store")
				assert.NotContains(t, w.Body.String(), "Feed API keys")
			})
		}
	}
}
