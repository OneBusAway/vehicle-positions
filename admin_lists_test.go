package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgeText(t *testing.T) {
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{0, "just now"},
		{1900 * time.Millisecond, "just now"},
		{2 * time.Second, "2 s ago"},
		{59 * time.Second, "59 s ago"},
		{time.Minute, "1 min ago"},
		{59*time.Minute + 59*time.Second, "59 min ago"},
		{time.Hour, "1 h ago"},
		{26 * time.Hour, "26 h ago"},
	} {
		assert.Equal(t, tc.want, ageText(tc.age), tc.age.String())
	}
}

func TestCountOf(t *testing.T) {
	assert.Equal(t, "0 vehicles", countOf(0, "vehicle", "vehicles"))
	assert.Equal(t, "1 vehicle", countOf(1, "vehicle", "vehicles"))
	assert.Equal(t, "2 vehicles", countOf(2, "vehicle", "vehicles"))
}

func TestNewPagerCount(t *testing.T) {
	assert.Equal(t, pagerCount{Text: "No keys"}, newPagerCount(0, "key", "keys"))
	assert.Equal(t, pagerCount{N: 1, Text: "1 key"}, newPagerCount(1, "key", "keys"))
	assert.Equal(t, pagerCount{N: 50, Text: "50 keys"}, newPagerCount(50, "key", "keys"))
}

func TestVehiclesHeadline(t *testing.T) {
	for _, tc := range []struct {
		total, reporting int
		want             string
	}{
		{0, 0, "No vehicles yet"},
		{1, 0, "1 vehicle, none reporting now"},
		{1, 1, "1 vehicle, 1 reporting now"},
		{14, 9, "14 vehicles, 9 reporting now"},
	} {
		assert.Equal(t, tc.want, vehiclesHeadline(tc.total, tc.reporting))
	}
}

func TestUsersHeadline(t *testing.T) {
	for _, tc := range []struct {
		drivers, admins, viewers int
		want                     string
	}{
		{0, 1, 0, "No drivers yet"},
		{0, 1, 2, "No drivers yet"},
		{1, 1, 0, "1 driver and 1 admin"},
		{11, 2, 0, "11 drivers and 2 admins"},
		{11, 1, 1, "11 drivers, 1 admin and 1 viewer"},
		{11, 2, 3, "11 drivers, 2 admins and 3 viewers"},
	} {
		assert.Equal(t, tc.want, usersHeadline(tc.drivers, tc.admins, tc.viewers))
	}
}

// TestVehiclesPage_HeadlineAndLastReport drives the list with one vehicle in
// each state the Last report column tells apart, and checks the headline
// counts a quiet vehicle as not reporting.
func TestVehiclesPage_HeadlineAndLastReport(t *testing.T) {
	now := time.Now()
	ui := newTestAdminUI(t)
	wireFakeVehicleStore(ui, newFakeVehicleStore(
		VehicleResponse{ID: "bus-live", Label: "Live bus", Active: true},
		VehicleResponse{ID: "bus-quiet", Label: "Quiet bus", Active: true},
		VehicleResponse{ID: "bus-waiting", Label: "Waiting bus", Active: true},
		VehicleResponse{ID: "bus-off", Label: "Parked bus", Active: true},
	))
	ui.stats = &fakeAdminStats{vehicles: 4}
	ui.alerts = twoAlerts(now)
	ui.activeTrips = &fakeActiveTrips{m: map[string]ActiveTripInfo{
		"bus-live":    {DriverName: "Amina"},
		"bus-quiet":   {DriverName: "Achieng"},
		"bus-waiting": {DriverName: "Baraka"},
	}}
	// The tracker still holds a quiet vehicle until the staleness threshold.
	for _, id := range []string{"bus-live", "bus-quiet"} {
		ui.tracker.Update(&LocationReport{VehicleID: id, TripID: "g1", Latitude: 1, Longitude: 2, Timestamp: now.Unix()})
	}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	body := getAdminPage(t, mux, "/admin/vehicles")

	assert.Contains(t, body, `<h2 class="h1">4 vehicles, 1 reporting now</h2>`)
	rows := strings.Split(body, "<tr>")[1:]
	require.Len(t, rows, 5, "the header row and four vehicles")
	row := func(label string) string {
		for _, r := range rows {
			if strings.Contains(r, ">"+label+"</a>") {
				return r
			}
		}
		t.Fatalf("no row for %s", label)
		return ""
	}
	assert.Regexp(t, `<span class="age"><span class="dot"></span>(just now|\d s ago)</span>`, row("Live bus"))
	assert.Contains(t, row("Live bus"), `data-label="Driver">Amina</td>`)
	assert.Contains(t, row("Quiet bus"), `<span class="dot dot-quiet"></span>Quiet 5 min</span>`)
	assert.Contains(t, row("Waiting bus"), `<span class="muted">No report yet</span>`)
	assert.Contains(t, row("Parked bus"), `<span class="muted">Not on a trip</span>`)
	assert.Contains(t, row("Parked bus"), `<td class="nowrap hide-phone"><span class="muted">none</span></td>`, "no driver")
}

func TestVehiclesPage_EmptyStates(t *testing.T) {
	ui := newTestAdminUI(t)
	wireFakeVehicleStore(ui, newFakeVehicleStore())
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	first := getAdminPage(t, mux, "/admin/vehicles")
	assert.Contains(t, first, `<h2 class="h1">No vehicles yet</h2>`)
	assert.Contains(t, first, "Add your first vehicle")
	assert.NotContains(t, first, "<table")

	searched := getAdminPage(t, mux, "/admin/vehicles?q=ghost")
	assert.Contains(t, searched, "No vehicle matches")
	assert.Contains(t, searched, `<a class="btn" href="/admin/vehicles">Clear the search</a>`)
	assert.NotContains(t, searched, "Add your first vehicle")
}

// TestVehiclesPage_EscapesWhatWasTyped: a label reaches the page as text, as
// a title and inside the confirm dialog's sentence, each escaped.
func TestVehiclesPage_EscapesWhatWasTyped(t *testing.T) {
	ui := newTestAdminUI(t)
	wireFakeVehicleStore(ui, newFakeVehicleStore(VehicleResponse{ID: "bus-1", Label: `<img src=x onerror="alert(1)">`, AgencyTag: `"><script>x</script>`, Active: true}))
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	body := getAdminPage(t, mux, "/admin/vehicles")

	assert.NotContains(t, body, "<img src=x")
	assert.NotContains(t, body, "<script>x</script>")
	assert.Contains(t, body, `title="&lt;img src=x onerror=&#34;alert(1)&#34;&gt;"`)
	assert.Contains(t, body, `data-confirm-text="&lt;img src=x onerror=&#34;alert(1)&#34;&gt; leaves the map at once`)
}

func TestVehicleForm_MarksTheFieldThatFailed(t *testing.T) {
	ui := newTestAdminUI(t)
	wireFakeVehicleStore(ui, newFakeVehicleStore())
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	blank := getAdminPage(t, mux, "/admin/vehicles/new")
	assert.Contains(t, blank, `<h1 class="topbar-title">New vehicle</h1>`)
	assert.Contains(t, blank, "up to 50 characters", "the help states the limit validateVehicleID enforces")
	assert.Equal(t, 50, maxVehicleIDLength)
	assert.NotContains(t, blank, "aria-invalid")
	assert.NotContains(t, blank, `role="alert"`)
	assert.NotContains(t, blank, "/deactivate", "a vehicle not yet saved has nothing to deactivate")

	w := postAdminForm(t, mux, "/admin/vehicles", url.Values{"id": {"bad id!"}, "label": {"Bus"}}.Encode())
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `value="bad id!" required aria-invalid="true" aria-describedby="id-err id-help"`)
	assert.Regexp(t, `<div class="field-error" id="id-err"><svg[^>]*><use href="`+assetURL(t, "icons.svg")+`#error"/></svg>vehicle id must contain only`, body)
	assert.Contains(t, body, `role="alert"`)
	assert.Contains(t, body, "The vehicle was not saved.</b> Fix the vehicle ID below.")
}

func TestVehicleEditPage_Actions(t *testing.T) {
	ui := newTestAdminUI(t)
	wireFakeVehicleStore(ui, newFakeVehicleStore(
		VehicleResponse{ID: "bus-1", Label: "Bus One", Active: true},
		VehicleResponse{ID: "bus-2", Label: "", Active: false},
	))
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	active := getAdminPage(t, mux, "/admin/vehicles/bus-1/edit")
	assert.Contains(t, active, `<h1 class="topbar-title">Edit vehicle</h1>`)
	assert.Contains(t, active, `<h2 class="h1">Bus One</h2>`)
	assert.Contains(t, active, `value="bus-1" readonly`)
	assert.Contains(t, active, `<form method="post" action="/admin/vehicles/bus-1/deactivate" data-confirm="Deactivate this vehicle?" data-confirm-text="Bus One leaves the map at once`)
	assert.Contains(t, active, `href="/api/v1/admin/vehicles/bus-1/locations?format=csv&amp;limit=1000"`)
	assert.NotContains(t, active, "/activate")

	inactive := getAdminPage(t, mux, "/admin/vehicles/bus-2/edit")
	assert.Contains(t, inactive, `<h2 class="h1">bus-2</h2>`, "a vehicle with no label goes by its id")
	assert.Contains(t, inactive, `<form method="post" action="/admin/vehicles/bus-2/activate">`)
	assert.NotContains(t, inactive, "/deactivate")
}

func TestUsersPage_HeadlineAndRows(t *testing.T) {
	ui := newTestAdminUI(t)
	users := newFakeUserStore(
		UserResponse{ID: 1, Name: "Ada Admin", Email: "ada@test.com", Role: roleAdmin, Active: true},
		UserResponse{ID: 2, Name: "Dana Driver", Email: "dana@test.com", Role: roleDriver, Active: true},
	)
	wireFakeUserStore(ui, users, newFakeAssignmentStore())
	ui.stats = &fakeAdminStats{drivers: 11, admins: 1}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	body := getAdminPage(t, mux, "/admin/users")

	assert.Contains(t, body, `<h1 class="topbar-title">Drivers and users</h1>`)
	assert.Contains(t, body, `<h2 class="h1">11 drivers and 1 admin</h2>`)
	assert.Contains(t, body, `<span class="avatar admin" aria-hidden="true">AA</span>`)
	assert.Contains(t, body, `<span class="avatar" aria-hidden="true">DD</span>`)
	assert.Contains(t, body, `data-label="May drive">0 vehicles</td>`)
	assert.Contains(t, body, `<a href="/admin/users?page=1" aria-current="true">Everyone</a>`)
	assert.Contains(t, body, `<form method="post" action="/admin/users/2/deactivate" data-confirm="Deactivate this account?" data-confirm-text="Dana Driver is signed out everywhere`)

}

// TestUsersPage_NothingMatches uses a store with no users, since the fake
// leaves matching q to the real store.
func TestUsersPage_NothingMatches(t *testing.T) {
	ui := newTestAdminUI(t)
	wireFakeUserStore(ui, newFakeUserStore(), newFakeAssignmentStore())
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	body := getAdminPage(t, mux, "/admin/users?q=nobody")

	assert.Contains(t, body, "No account matches")
	assert.Contains(t, body, `<a class="btn" href="/admin/users">Clear the search</a>`)
	assert.NotContains(t, body, "<table")
}

func TestUserForm_MarksTheFieldThatFailed(t *testing.T) {
	ui := newTestAdminUI(t)
	wireFakeUserStore(ui, newFakeUserStore(UserResponse{ID: 1, Name: "Ada Admin", Email: "ada@test.com", Role: roleAdmin, Active: true}), newFakeAssignmentStore())
	ui.stats = &fakeAdminStats{admins: 1}
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	blank := getAdminPage(t, mux, "/admin/users/new")
	assert.Contains(t, blank, `<h1 class="topbar-title">New user</h1>`)
	assert.Contains(t, blank, "At least 8 characters.", "the help states the minimum validatePassword enforces")
	assert.Equal(t, 8, minPasswordLength)
	assert.NotContains(t, blank, "aria-invalid")

	valid := url.Values{"name": {"New Guy"}, "email": {"newguy@test.com"}, "password": {"password123"}, "role": {roleDriver}}
	with := func(key, value string) string {
		form := url.Values{}
		for k, v := range valid {
			form[k] = v
		}
		form.Set(key, value)
		return form.Encode()
	}
	for _, tc := range []struct {
		name, path, form, field, message string
	}{
		{"create, no name", "/admin/users", with("name", ""), "name", "name is required"},
		{"create, bad email", "/admin/users", with("email", "newguy@"), "email", "email is not valid"},
		{"create, taken email", "/admin/users", with("email", "ada@test.com"), "email", "email already exists"},
		{"create, short password", "/admin/users", with("password", "short"), "password", "password must be at least 8 characters"},
		{"create, bad role", "/admin/users", with("role", "boss"), "role", "role must be driver, admin or viewer"},
		{"edit, no email", "/admin/users/1", with("email", ""), "email", "email is required"},
		{"edit, short password", "/admin/users/1", with("password", "short"), "password", "password must be at least 8 characters"},
		{"edit, last admin demoted", "/admin/users/1", with("email", "ada@test.com"), "role", "cannot demote the last active admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := postAdminForm(t, mux, tc.path, tc.form)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code)
			body := w.Body.String()
			assert.Equal(t, 1, strings.Count(body, `aria-invalid="true"`), "one field is marked")
			assert.Regexp(t, `id="`+tc.field+`"[^>]* aria-invalid="true" aria-describedby="`+tc.field+`-err`, body)
			assert.Regexp(t, `<div class="field-error" id="`+tc.field+`-err"><svg[^>]*><use [^>]*></svg>`+tc.message+`</div>`, body)
			assert.Contains(t, body, "The account was not saved.</b> Fix the field marked below.")
			assert.NotContains(t, body, "/deactivate", "a form shown again with an error does not know the account's state")
		})
	}
}

func TestUserEditPage_Actions(t *testing.T) {
	ui := newTestAdminUI(t)
	wireFakeUserStore(ui, newFakeUserStore(
		UserResponse{ID: 1, Name: "Ada Admin", Email: "ada@test.com", Role: roleAdmin, Active: true},
		UserResponse{ID: 2, Name: "Dana Driver", Email: "dana@test.com", Role: roleDriver, Active: true},
		UserResponse{ID: 3, Name: "Gone Driver", Email: "gone@test.com", Role: roleDriver, Active: false},
	), newFakeAssignmentStore())
	wireFakeVehicleStore(ui, newFakeVehicleStore())
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	driver := getAdminPage(t, mux, "/admin/users/2/edit")
	assert.Contains(t, driver, `<h1 class="topbar-title">Edit user</h1>`)
	assert.Contains(t, driver, `<h2 class="h1">Dana Driver</h2>`)
	assert.Contains(t, driver, `<a class="btn btn-tonal" href="/admin/driver-setup?user=2">`)
	assert.Contains(t, driver, `action="/admin/users/2/deactivate" data-confirm="Deactivate this account?"`)
	assert.Contains(t, driver, "No vehicles assigned.")
	assert.Contains(t, driver, "No more active vehicles to assign.")

	admin := getAdminPage(t, mux, "/admin/users/1/edit")
	assert.NotContains(t, admin, "/admin/driver-setup?user=1", "admins don't drive")
	assert.Contains(t, admin, `action="/admin/users/1/deactivate"`)

	gone := getAdminPage(t, mux, "/admin/users/3/edit")
	assert.NotContains(t, gone, "/admin/driver-setup?user=3", "a deactivated driver can't sign in")
	assert.Contains(t, gone, `<form method="post" action="/admin/users/3/activate">`)
	assert.NotContains(t, gone, "/deactivate")
}
