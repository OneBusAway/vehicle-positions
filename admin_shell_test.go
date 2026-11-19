package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// railState is how the menu shows one page's link.
type railState string

const (
	railMissing railState = "missing"
	railLinked  railState = "linked"
	railOpen    railState = "open"
)

// railLink reports how the rail in body shows the link to href: not there,
// there, or there and marked as the page that is open.
func railLink(body, href string) railState {
	m := regexp.MustCompile(`<a class="rail-item" href="` + regexp.QuoteMeta(href) + `"( aria-current="page")? `).FindStringSubmatch(body)
	switch {
	case m == nil:
		return railMissing
	case m[1] != "":
		return railOpen
	default:
		return railLinked
	}
}

func newShellMux(t *testing.T) (*adminUI, *http.ServeMux) {
	t.Helper()
	ui := newTestAdminUI(t)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	return ui, mux
}

func TestShell_MenuListsEveryPageAndMarksOne(t *testing.T) {
	_, mux := newShellMux(t)

	for _, tc := range []struct {
		path string
		open string
	}{
		{"/admin/dashboard", "/admin/dashboard"},
		{"/admin/map", "/admin/map"},
		{"/admin/vehicles/new", "/admin/vehicles"},
		{"/admin/health", "/admin/health"},
		{"/admin/more", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			body := getAdminPage(t, mux, tc.path)
			for _, group := range adminNav {
				assert.Contains(t, body, `<div class="rail-group">`+group.Title+`</div>`)
				for _, item := range group.Items {
					want := railLinked
					if item.Href == tc.open {
						want = railOpen
					}
					assert.Equal(t, want, railLink(body, item.Href), item.Label)
					assert.Contains(t, body, `<span class="rail-label">`+item.Label+`</span>`)
				}
			}
			assert.Equal(t, strings.Count(body, `aria-current="page"`), map[bool]int{true: 2, false: 1}[tc.path == "/admin/dashboard" || tc.path == "/admin/map"],
				"one rail item, plus the bottom bar's tab when the page has one")
		})
	}
}

func TestShell_BrandAndTitle(t *testing.T) {
	_, mux := newShellMux(t)

	body := getAdminPage(t, mux, "/admin/vehicles")

	assert.Contains(t, body, "<title>Vehicles · Vehicle Tracker</title>")
	assert.Contains(t, body, `<h1 class="topbar-title">Vehicles</h1>`)
	assert.Contains(t, body, `<a class="skip" href="#main">Skip to content</a>`)
	assert.Contains(t, body, `<main class="main" id="main" tabindex="-1">`)
	assert.NotContains(t, body, "Transit Tracker")
	assert.NotContains(t, body, "Fleet Operations")
}

// TestShell_LeafletOnlyWhereThereIsAMap: Leaflet's stylesheet is not in a
// cascade layer, so admin.css has to come after it to restyle it.
func TestShell_LeafletOnlyWhereThereIsAMap(t *testing.T) {
	_, mux := newShellMux(t)

	plain := getAdminPage(t, mux, "/admin/vehicles")
	assert.NotContains(t, plain, "leaflet")
	assert.Contains(t, plain, `<script src="/static/js/app.js" defer></script>`)

	withMap := getAdminPage(t, mux, "/admin/map")
	leaflet := strings.Index(withMap, `href="/static/vendor/leaflet/leaflet.css"`)
	admin := strings.Index(withMap, `href="/static/css/admin.css"`)
	require.NotEqual(t, -1, leaflet)
	require.NotEqual(t, -1, admin)
	assert.Less(t, leaflet, admin, "admin.css must load after leaflet.css")
	assert.Contains(t, withMap, `<script src="/static/vendor/leaflet/leaflet.js" defer></script>`)
	assert.Contains(t, withMap, `<script src="/static/js/admin.js" defer></script>`)
}

// TestShell_Pulse: a vehicle that has gone quiet is still in the tracker
// until the staleness threshold passes, and must be counted as quiet, not as
// reporting as well.
func TestShell_Pulse(t *testing.T) {
	now := time.Now()
	report := func(ui *adminUI, id string) {
		ui.tracker.Update(&LocationReport{VehicleID: id, TripID: "g1", Latitude: 1, Longitude: 2, Timestamp: now.Unix()})
	}

	t.Run("reporting and quiet", func(t *testing.T) {
		ui, mux := newShellMux(t)
		ui.alerts = twoAlerts(now)
		report(ui, "bus-quiet")
		report(ui, "bus-still")
		report(ui, "bus-fine")

		body := getAdminPage(t, mux, "/admin/vehicles")

		assert.Contains(t, body, `id="pulse" data-state="live"`)
		assert.Contains(t, body, `<span id="pulse-reporting">2</span>`)
		assert.Contains(t, body, `<span class="pulse-q" id="pulse-q">`)
		assert.Contains(t, body, `<span id="pulse-quiet">1</span> quiet`)
	})

	t.Run("nothing reporting", func(t *testing.T) {
		_, mux := newShellMux(t)

		body := getAdminPage(t, mux, "/admin/vehicles")

		assert.Contains(t, body, `id="pulse" data-state="idle"`)
		assert.Contains(t, body, `<span id="pulse-reporting">0</span>`)
		assert.Contains(t, body, `<span class="pulse-q" id="pulse-q" hidden>`, "no quiet count to show")
	})
}

func TestShell_AlertCountInThreePlaces(t *testing.T) {
	t.Run("with alerts", func(t *testing.T) {
		mux := newAlertsMux(t, twoAlerts(time.Now()))

		body := getAdminPage(t, mux, "/admin/vehicles")

		assert.Equal(t, 3, strings.Count(body, `data-alert-count>2</span>`), "the rail, the bell and the bottom bar")
		assert.Equal(t, 3, strings.Count(body, `aria-label="Alerts, 2 open" data-alert-link`))
	})

	t.Run("without alerts", func(t *testing.T) {
		mux := newAlertsMux(t, newAlertWatcher(&fakeTripLister{}, testAlertSettings))

		body := getAdminPage(t, mux, "/admin/vehicles")

		assert.Equal(t, 3, strings.Count(body, `data-alert-count hidden></span>`))
		assert.Equal(t, 3, strings.Count(body, `aria-label="Alerts" data-alert-link`))
	})
}

func TestShell_AccountMenu(t *testing.T) {
	t.Run("name and email from the user's row", func(t *testing.T) {
		ui, mux := newShellMux(t)
		// cookieFor signs in user 9.
		wireFakeUserStore(ui, newFakeUserStore(UserResponse{ID: 9, Name: "Wanjiru Kamau<b>x</b>", Email: "wanjiru@agency.example", Role: roleAdmin, Active: true}), newFakeAssignmentStore())

		body := getAdminPage(t, mux, "/admin/vehicles")

		assert.Contains(t, body, `aria-label="Account">WK</button>`)
		assert.Contains(t, body, `Wanjiru Kamau&lt;b&gt;x&lt;/b&gt;`, "a name is escaped")
		assert.NotContains(t, body, "<b>x</b>")
		assert.Contains(t, body, `wanjiru@agency.example`)
		assert.Contains(t, body, `<form method="post" action="/admin/logout">`)
	})

	t.Run("the session's email when its user is gone", func(t *testing.T) {
		logs := captureLogs(t)
		ui, mux := newShellMux(t)
		wireFakeUserStore(ui, newFakeUserStore(), newFakeAssignmentStore())

		body := getAdminPage(t, mux, "/admin/vehicles")

		assert.Contains(t, body, `aria-label="Account">A</button>`)
		assert.Contains(t, body, "admin@test.com")
		assert.Contains(t, logs.String(), `level=WARN msg="account menu: the session's user no longer exists" user_id=9`)
		assert.NotContains(t, logs.String(), "level=ERROR")
	})

	t.Run("the session's email when the database fails, logged as the server's fault", func(t *testing.T) {
		logs := captureLogs(t)
		ui, mux := newShellMux(t)
		ui.userManager = failingUserGetter{newFakeUserStore()}

		body := getAdminPage(t, mux, "/admin/vehicles")

		assert.Contains(t, body, `aria-label="Account">A</button>`)
		assert.Contains(t, body, "admin@test.com")
		assert.Contains(t, logs.String(), `level=ERROR msg="account menu: read user" user_id=9 error="connection refused"`)
		assert.NotContains(t, body, "connection refused")
	})
}

func TestInitials(t *testing.T) {
	for _, tc := range []struct {
		name, email, want string
	}{
		{"Test Admin", "admin@test.com", "TA"},
		{"asha", "asha@agency.example", "A"},
		{"Mary Wanjiru Kamau", "m@agency.example", "MW"},
		{"  Élodie   dupont ", "e@agency.example", "ÉD"},
		{"", "driver@test.com", "D"},
		{"", "", ""},
	} {
		assert.Equal(t, tc.want, initials(tc.name, tc.email), "%q %q", tc.name, tc.email)
	}
}

func TestShell_FlashIsAToast(t *testing.T) {
	_, mux := newShellMux(t)
	get := func(code string) string {
		req := httptest.NewRequest(http.MethodGet, "/admin/vehicles", nil)
		req.AddCookie(cookieFor(t, "admin"))
		req.AddCookie(&http.Cookie{Name: flashCookieName, Value: code})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}

	done := get("vehicle_created")
	assert.Contains(t, done, `<div class="toast ok" role="status" data-toast>`)
	assert.Contains(t, done, "<span>Vehicle created.</span>")

	failed := get("unassign_on_trip")
	assert.Contains(t, failed, `<div class="toast" role="status" data-toast data-toast-stay>`, "a message that something did not go through stays until it is closed")
	assert.Contains(t, failed, "/static/icons.svg#warning")

	assert.NotContains(t, get("no-such-code"), "data-toast")
}

func TestFlashMessages_WarnOnlyWhatDidNotGoThrough(t *testing.T) {
	var warn []string
	for code, f := range flashMessages {
		require.NotEmpty(t, f.Text, code)
		if f.Warn {
			warn = append(warn, code)
		}
	}
	assert.ElementsMatch(t, []string{"unassign_on_trip", "logout_failed"}, warn)
}

func TestMorePage(t *testing.T) {
	ui, mux := newShellMux(t)
	wireFakeUserStore(ui, newFakeUserStore(UserResponse{ID: 9, Name: "Test Admin", Email: "admin@test.com", Role: roleAdmin, Active: true}), newFakeAssignmentStore())

	body := getAdminPage(t, mux, "/admin/more")

	assert.Contains(t, body, "<title>Menu · Vehicle Tracker</title>")
	assert.Contains(t, body, `<a href="/admin/more" aria-current="page">`, "the bottom bar marks More")
	for _, group := range adminNav {
		assert.Contains(t, body, `<h2>`+group.Title+`</h2>`)
		for _, item := range group.Items {
			assert.Contains(t, body, `<a class="line-row more-row" href="`+item.Href+`">`, item.Label)
		}
	}
	assert.Contains(t, body, "<h2>Test Admin, admin@test.com</h2>")
	assert.Contains(t, body, `<form method="post" action="/admin/logout"><button class="line-row more-row" type="submit">`, "signing out works there without JavaScript")
}

func TestQuietVehicles(t *testing.T) {
	now := time.Now()
	quiet := alertEntry{Kind: alertQuiet, DurationSeconds: 240, Trip: alertTrip("bus-2", now)}
	alerts := []alertEntry{
		{Kind: alertNotMoving, Trip: alertTrip("bus-1", now)},
		quiet,
		{Kind: alertOffRoute, Trip: alertTrip("bus-3", now)},
	}

	assert.Equal(t, map[string]alertEntry{"bus-2": quiet}, quietVehicles(alerts), "only a quiet alert makes a vehicle quiet")
	assert.Empty(t, quietVehicles(nil))
}

// TestFleetNow_AgreesWithTheOverview: the top bar and the Overview count the
// same vehicles as reporting and as quiet.
func TestFleetNow_AgreesWithTheOverview(t *testing.T) {
	ix := reportFixtureIndex(t)
	in := overviewTestInput(t, ix)
	ui := newTestAdminUI(t)
	for _, v := range in.tracked {
		ui.tracker.Update(&LocationReport{VehicleID: v.VehicleID, Timestamp: time.Now().Unix()})
	}

	bar := ui.fleetNow(in.alerts)
	overview := newOverviewView(in)

	assert.Equal(t, fleetCounts{Reporting: 2, Quiet: 1}, bar)
	assert.Equal(t, bar.Reporting, overview.Reporting)
	assert.Equal(t, bar.Quiet, overview.Quiet)
}

func TestStepLine_OneStep(t *testing.T) {
	line := stepLine{N: 1, Of: 1}
	assert.Equal(t, []stepStop{{Left: "0.0"}}, line.Stops())
	assert.Equal(t, "0.0", line.VehicleLeft())
}
