package main

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exampleSetupLink is the link for https://tracker.example.org exactly as the
// apps' tests build it and accept it: link("https://tracker.example.org") in
// android/.../ServerSetupLinkTest.kt and ios/.../ServerSetupLinkTests.swift,
// which percent-encode every byte but a letter or a digit.
const exampleSetupLink = "obavehicletracker://setup?server=https%3A%2F%2Ftracker%2Eexample%2Eorg"

// serverFromSetupLink reads a setup link the way both apps' ServerSetupLink.parse
// do, short of their https rule: scheme obavehicletracker, host setup, and
// exactly one query item, server, percent-decoded with "+" kept as a plus.
func serverFromSetupLink(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	require.NoError(t, err)
	require.Equal(t, "obavehicletracker", u.Scheme)
	require.Equal(t, "setup", u.Host)
	items := strings.Split(u.RawQuery, "&")
	require.Len(t, items, 1, "the apps refuse a link with more than one query item")
	name, value, ok := strings.Cut(items[0], "=")
	require.True(t, ok)
	require.Equal(t, "server", name)
	server, err := url.PathUnescape(value)
	require.NoError(t, err)
	return server
}

func TestSetupLink(t *testing.T) {
	cases := []struct {
		server string
		want   string
	}{
		{"https://tracker.example.org", exampleSetupLink},
		// link("https://tracker.example.org/vp") in both apps' tests.
		{"https://tracker.example.org/vp", "obavehicletracker://setup?server=https%3A%2F%2Ftracker%2Eexample%2Eorg%2Fvp"},
		{"http://localhost:8080", "obavehicletracker://setup?server=http%3A%2F%2Flocalhost%3A8080"},
		// Left raw, "&" would start a second query item, "+" and "%" would
		// decode to something else, and "#" would cut the link short.
		{"https://tracker.example.org/a+b&c=d%e f#g", "obavehicletracker://setup?server=https%3A%2F%2Ftracker%2Eexample%2Eorg%2Fa%2Bb%26c%3Dd%25e%20f%23g"},
	}
	for _, tc := range cases {
		t.Run(tc.server, func(t *testing.T) {
			link := setupLink(tc.server)
			assert.Equal(t, tc.want, link)
			assert.Equal(t, tc.server, serverFromSetupLink(t, link))
		})
	}
}

// requestDriverSetup sends target to the admin UI's mux with an admin session.
// A target with a host sets the request's Host, and https in it marks the
// request as having come over TLS, as httptest.NewRequest does.
func requestDriverSetup(t *testing.T, ui *adminUI, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

var (
	setupLinkPattern   = regexp.MustCompile(`obavehicletracker://[^<"\s]*`)
	serverInputPattern = regexp.MustCompile(`<input id="server" name="server"[^>]* value="([^"]*)"`)
	qrViewBoxPattern   = regexp.MustCompile(`<svg [^>]*viewBox="0 0 (\d+) (\d+)"[^>]* aria-label="` + qrLabel + `"`)
	qrPathPattern      = regexp.MustCompile(`<rect width="\d+" height="\d+" fill="#fff"/><path fill="#000" d="([^"]*)"`)
	qrRunPattern       = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1h-(\d+)z`)
)

// pageSetupLinks is every setup link the page shows, unescaped.
func pageSetupLinks(body string) []string {
	return setupLinkPattern.FindAllString(html.UnescapeString(body), -1)
}

func pageServerInput(t *testing.T, body string) string {
	t.Helper()
	m := serverInputPattern.FindStringSubmatch(body)
	require.NotNil(t, m, "the page must have the server address field")
	return html.UnescapeString(m[1])
}

// pageQRModules reads the page's QR code back into a grid of modules, one
// subpath per run of dark modules, as qrSVG draws it.
func pageQRModules(t *testing.T, body string) [][]bool {
	t.Helper()
	box := qrViewBoxPattern.FindStringSubmatch(body)
	require.NotNil(t, box, "the page must have a QR code")
	require.Equal(t, box[1], box[2], "a QR code is square")
	size, err := strconv.Atoi(box[1])
	require.NoError(t, err)

	grid := make([][]bool, size)
	for y := range grid {
		grid[y] = make([]bool, size)
	}
	path := qrPathPattern.FindStringSubmatch(body)
	require.NotNil(t, path)
	runs := qrRunPattern.FindAllStringSubmatch(path[1], -1)
	require.NotEmpty(t, runs)
	require.Equal(t, path[1], strings.Join(qrRunPattern.FindAllString(path[1], -1), ""), "the path must hold nothing but runs")
	for _, run := range runs {
		x, _ := strconv.Atoi(run[1])
		y, _ := strconv.Atoi(run[2])
		width, _ := strconv.Atoi(run[3])
		require.Equal(t, run[3], run[4], "a run must close back to where it began")
		for i := 0; i < width; i++ {
			require.False(t, grid[y][x+i], "runs must not overlap")
			grid[y][x+i] = true
		}
	}
	return grid
}

// assertQREncodes checks the page's QR code module for module against the
// code for want, margin included.
func assertQREncodes(t *testing.T, body, want string) {
	t.Helper()
	q, err := qrcode.New(want, qrcode.Medium)
	require.NoError(t, err)
	assert.Equal(t, q.Bitmap(), pageQRModules(t, body), "the QR code must encode %q", want)
}

func TestDriverSetupPage_RequiresAdminSession(t *testing.T) {
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	for _, role := range []string{"", "driver"} {
		t.Run("role "+role, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/driver-setup", nil)
			if role != "" {
				req.AddCookie(cookieFor(t, role))
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusSeeOther, w.Code)
			assert.Equal(t, loginLocation(http.MethodGet, "/admin/driver-setup"), w.Header().Get("Location"))
		})
	}

	t.Run("admin", func(t *testing.T) {
		w := requestDriverSetup(t, ui, "/admin/driver-setup", nil)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, railOpen, railLink(w.Body.String(), "/admin/driver-setup"), "the menu must mark Driver setup as the page open")
	})
}

func TestDriverSetupPage_AddressFromRequest(t *testing.T) {
	https := http.Header{"X-Forwarded-Proto": {"https"}}
	cases := []struct {
		name       string
		trustProxy bool
		target     string
		header     http.Header
		want       string
	}{
		{"no proxy, plain http", false, "http://tracker.example.org:8080/admin/driver-setup", nil, "http://tracker.example.org:8080"},
		{"no proxy, TLS", false, "https://tracker.example.org/admin/driver-setup", nil, "https://tracker.example.org"},
		{"trusted proxy serving https", true, "http://tracker.example.org/admin/driver-setup", https, "https://tracker.example.org"},
		{"trusted proxy serving http", true, "http://tracker.example.org/admin/driver-setup", http.Header{"X-Forwarded-Proto": {"http"}}, "http://tracker.example.org"},
		// Without TRUST_PROXY_HEADERS anyone can send the header, as
		// requestIsSecure has it for the session cookie.
		{"untrusted X-Forwarded-Proto", false, "http://tracker.example.org/admin/driver-setup", https, "http://tracker.example.org"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := newTestAdminUI(t)
			ui.cfg.trustProxy = tc.trustProxy

			w := requestDriverSetup(t, ui, tc.target, tc.header)

			require.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()
			assert.Equal(t, tc.want, pageServerInput(t, body))
			assert.Equal(t, []string{setupLink(tc.want)}, pageSetupLinks(body))
			// Both apps ask about the host alone, without the port.
			assert.Contains(t, body, "Use tracker.example.org?")
		})
	}
}

func TestDriverSetupPage_QREncodesTheLink(t *testing.T) {
	ui := newTestAdminUI(t)

	w := requestDriverSetup(t, ui, "https://tracker.example.org/admin/driver-setup", nil)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Equal(t, []string{exampleSetupLink}, pageSetupLinks(body))
	assertQREncodes(t, body, exampleSetupLink)
	assert.Contains(t, body, "<b>Use tracker.example.org?</b>", "the sheet must name the host the app asks about")
	assert.Contains(t, body, `<div class="phone-ask">Use tracker.example.org?</div>`, "and so must the preview of the app's question")
	assert.Contains(t, body, `<h2 class="h1">Hand each driver this sheet</h2>`)
	assert.Contains(t, body, `<div class="setup-link" id="setup-link">`+exampleSetupLink+`</div>`)
	assert.Contains(t, body, `data-copy-from="setup-link"`, "Copy reads the link from the page, so the link is on it once")
	assert.Contains(t, body, `<div class="page-actions no-print js-only"><button class="btn btn-primary" type="button" onclick="window.print()">`)
	assert.Contains(t, body, `<article class="sheet" aria-label="Sheet for the driver">`)
	assert.Contains(t, body, `<div class="sheet-fallback">If the code won't scan, type this address as the server on the sign-in screen: <b>https://tracker.example.org</b></div>`)
}

func TestDriverSetupPage_TypedAddress(t *testing.T) {
	ui := newTestAdminUI(t)
	typed := "https://vp.agency.example/tracker"

	w := requestDriverSetup(t, ui, "http://127.0.0.1:8080/admin/driver-setup?server="+url.QueryEscape("  "+typed+" "), nil)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Equal(t, typed, pageServerInput(t, body), "a typed address replaces the request's, trimmed")
	link := "obavehicletracker://setup?server=https%3A%2F%2Fvp%2Eagency%2Eexample%2Ftracker"
	assert.Equal(t, []string{link}, pageSetupLinks(body))
	assertQREncodes(t, body, link)
	assert.Contains(t, body, "Use vp.agency.example?")
	assert.NotContains(t, body, "TRUST_PROXY_HEADERS")
}

func TestDriverSetupPage_PlainHTTPWarns(t *testing.T) {
	ui := newTestAdminUI(t)

	w := requestDriverSetup(t, ui, "http://tracker.example.org/admin/driver-setup", nil)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Drivers' phones take only an https:// address, so the apps will not use this link.")
	assert.Contains(t, body, "TRUST_PROXY_HEADERS=true")
	assertQREncodes(t, body, setupLink("http://tracker.example.org"))
}

func TestDriverSetupPage_RefusesAddress(t *testing.T) {
	cases := []struct {
		name   string
		server string
		want   string
	}{
		{"no scheme", "tracker.example.org", "type the server&#39;s full address, such as https://tracker.example.org"},
		{"another scheme", "ftp://tracker.example.org", "type the server&#39;s full address, such as https://tracker.example.org"},
		{"no host", "https://", "type the server&#39;s full address, such as https://tracker.example.org"},
		{"unparseable", "https://tracker example.org", "type the server&#39;s full address, such as https://tracker.example.org"},
		{"user info", "https://tracker.example.org@evil.example", "leave the user name and password out of the address: the apps refuse an address that has them"},
		{"too long for a QR code", "https://" + strings.Repeat("a", 3000) + ".example", "the address is too long to put in a QR code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := newTestAdminUI(t)

			w := requestDriverSetup(t, ui, "https://tracker.example.org/admin/driver-setup?server="+url.QueryEscape(tc.server), nil)

			require.Equal(t, http.StatusUnprocessableEntity, w.Code)
			body := w.Body.String()
			assert.Contains(t, body, tc.want)
			assert.Equal(t, tc.server, pageServerInput(t, body), "the field keeps what was typed so it can be fixed")
			assert.Empty(t, pageSetupLinks(body), "no link for an address the apps refuse")
			assert.NotContains(t, body, qrLabel, "no QR code for an address the apps refuse")
			assert.Contains(t, body, `aria-describedby="srv-help srv-err" aria-invalid="true">`)
			assert.Contains(t, body, `<div class="field-error" id="srv-err">`)
			assert.NotContains(t, body, `<article class="sheet"`, "nor a sheet to print")
			assert.NotContains(t, body, "window.print()")
		})
	}
}

func TestDriverSetupPage_DriverSheet(t *testing.T) {
	ui := newTestAdminUI(t)
	users := newFakeUserStore(UserResponse{ID: 7, Name: "Asha Rao", Email: "asha.rao@agency.example", Role: roleDriver, Active: true})
	wireFakeUserStore(ui, users, newFakeAssignmentStore())
	w := requestDriverSetup(t, ui, "https://tracker.example.org/admin/driver-setup?user=7", nil)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `<div class="sheet-for">For Asha Rao</div>`)
	assert.Contains(t, body, "Sign in with your email address, <b>asha.rao@agency.example</b>, and your password.")
	assert.Contains(t, body, `<input type="hidden" name="user" value="7">`, "changing the address must keep the driver's sheet")
	// The apps take only the server from a link, so the driver's sheet has
	// the same link and code as anyone's, and nothing secret.
	assert.Equal(t, []string{exampleSetupLink}, pageSetupLinks(body))
	assertQREncodes(t, body, exampleSetupLink)
}

func TestDriverSetupPage_GeneralSheetHasNoEmail(t *testing.T) {
	ui := newTestAdminUI(t)

	w := requestDriverSetup(t, ui, "https://tracker.example.org/admin/driver-setup", nil)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "the email address your agency gave you")
	assert.NotContains(t, body, `name="user"`)
	assert.NotContains(t, body, `class="sheet-for"`)
}

// TestDriverSetupPage_EscapesTheDriver: a driver's name and email are what an
// admin typed, and the address is what this one did.
func TestDriverSetupPage_EscapesTheDriver(t *testing.T) {
	ui := newTestAdminUI(t)
	users := newFakeUserStore(UserResponse{ID: 7, Name: `<img src=x onerror=alert(1)>`, Email: `"><script>alert(2)</script>@agency.example`, Role: roleDriver, Active: true})
	wireFakeUserStore(ui, users, newFakeAssignmentStore())

	w := requestDriverSetup(t, ui, "https://tracker.example.org/admin/driver-setup?user=7&server="+url.QueryEscape(`https://tracker.example.org/<b>"x"</b>`), nil)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, "<img src=x")
	assert.NotContains(t, body, "<script>alert(2)")
	assert.NotContains(t, body, `<b>"x"</b>`)
	assert.Contains(t, body, `<div class="sheet-for">For &lt;img src=x onerror=alert(1)&gt;</div>`)
	assert.Contains(t, body, `<b>https://tracker.example.org/&lt;b&gt;&#34;x&#34;&lt;/b&gt;</b>`)
}

// failingUserGetter is a user store whose lookups fail, as a lost database
// connection does.
type failingUserGetter struct {
	*fakeUserStore
}

func (failingUserGetter) GetUser(context.Context, int64) (*UserResponse, error) {
	return nil, errors.New("connection refused")
}

func TestDriverSetupPage_UserLookup(t *testing.T) {
	cases := []struct {
		name  string
		query string
		fail  bool
		want  int
	}{
		{"unknown user", "?user=99", false, http.StatusNotFound},
		{"not an id", "?user=asha", false, http.StatusNotFound},
		{"store error", "?user=7", true, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := newTestAdminUI(t)
			users := newFakeUserStore(UserResponse{ID: 7, Name: "Asha Rao", Email: "asha.rao@agency.example", Role: roleDriver, Active: true})
			wireFakeUserStore(ui, users, newFakeAssignmentStore())
			if tc.fail {
				ui.userManager = failingUserGetter{users}
			}

			w := requestDriverSetup(t, ui, "/admin/driver-setup"+tc.query, nil)

			assert.Equal(t, tc.want, w.Code)
			assert.NotContains(t, w.Body.String(), "asha.rao@agency.example")
		})
	}
}

func TestUsersPage_SetupSheetLink(t *testing.T) {
	ui := newTestAdminUI(t)
	users := newFakeUserStore(
		UserResponse{ID: 1, Name: "Active Driver", Email: "active@agency.example", Role: roleDriver, Active: true},
		UserResponse{ID: 2, Name: "Gone Driver", Email: "gone@agency.example", Role: roleDriver, Active: false},
		UserResponse{ID: 3, Name: "An Admin", Email: "admin@agency.example", Role: roleAdmin, Active: true},
	)
	wireFakeUserStore(ui, users, newFakeAssignmentStore())
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	body := getAdminPage(t, mux, "/admin/users")

	assert.Contains(t, body, `href="/admin/driver-setup?user=1"`, "an active driver gets a setup sheet")
	assert.NotContains(t, body, `href="/admin/driver-setup?user=2"`, "a deactivated driver can't sign in")
	assert.NotContains(t, body, `href="/admin/driver-setup?user=3"`, "admins don't drive")
}
