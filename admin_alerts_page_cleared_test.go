package main

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClearedAlerts is a ClearedAlertLister that returns alerts as they are
// and records what it was asked for.
type fakeClearedAlerts struct {
	mu     sync.Mutex
	alerts []clearedAlert
	err    error
	kind   string
	limit  int
	offset int
}

func (f *fakeClearedAlerts) ListClearedAlerts(_ context.Context, kind string, limit, offset int) ([]clearedAlert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kind, f.limit, f.offset = kind, limit, offset
	return f.alerts, f.err
}

// newClearedAlertsMux serves the admin UI with no open alert and cleared as
// the cleared ones.
func newClearedAlertsMux(t *testing.T, cleared ClearedAlertLister) *http.ServeMux {
	t.Helper()
	ui := newTestAdminUI(t)
	ui.clearedAlerts = cleared
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	return mux
}

// clearedAt is a cleared alert of kind on vehicleID's trip 7 on route R1,
// raised at raised and lasting lasted.
func clearedAt(kind, vehicleID string, raised time.Time, lasted time.Duration) clearedAlert {
	return clearedAlert{
		Kind:         kind,
		VehicleID:    vehicleID,
		VehicleLabel: "Bus " + strings.TrimPrefix(vehicleID, "bus-"),
		TripID:       7,
		GtfsTripID:   "T1",
		RouteID:      "R1",
		RaisedAt:     raised,
		ClearedAt:    raised.Add(lasted),
	}
}

// clearedRowPattern matches a cleared alert's row and captures its vehicle
// and kind.
var clearedRowPattern = regexp.MustCompile(`<td class="c-main strong nowrap" title="([^"]+)">[^<]*</td>\s*<td class="c-side"><span class="pill plain">([^<]+)</span>`)

// clearedRows are the cleared alerts' rows on a page, in order: vehicle and
// kind.
func clearedRows(body string) [][2]string {
	var rows [][2]string
	for _, m := range clearedRowPattern.FindAllStringSubmatch(body, -1) {
		rows = append(rows, [2]string{m[1], m[2]})
	}
	return rows
}

func TestAlertsPage_ListsClearedAlerts(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	cleared := &fakeClearedAlerts{alerts: []clearedAlert{
		clearedAt(alertOffRoute, "bus-12", today.Add(9*time.Hour+2*time.Minute), 18*time.Minute),
		clearedAt(alertNotStarted, "bus-4", today.Add(8*time.Hour+30*time.Minute), 95*time.Minute),
	}}
	mux := newClearedAlertsMux(t, cleared)

	body := getAdminPage(t, mux, "/admin/alerts")

	assert.Equal(t, "", cleared.kind, "every kind")
	assert.Equal(t, adminPageSize+1, cleared.limit, "one row past the page, to know there is a next")
	assert.Equal(t, 0, cleared.offset)
	assert.Contains(t, body, `<section class="card" id="cleared"`)
	assert.Contains(t, body, "Newest first. Times are UTC time.")
	assert.Equal(t, [][2]string{{"bus-12", "Off route"}, {"bus-4", "Not started"}}, clearedRows(body), "in the store's order")
	assert.Contains(t, body, ">Bus 12</td>")
	assert.Contains(t, body, `<span class="route">R1</span>`)
	assert.Contains(t, body, "trip T1")
	assert.Contains(t, body, `data-label="Raised">09:02<span class="sub">`+today.Format("2 Jan")+`</span>`)
	assert.Contains(t, body, `data-label="Cleared">09:20</td>`)
	assert.Contains(t, body, `data-label="Lasted">18 min</td>`)
	assert.Contains(t, body, `data-label="Lasted">1 h 35 min</td>`)
	assert.Contains(t, body, `href="/admin/trips/7" aria-label="Open trip 7"`)
	assert.Contains(t, body, `<a class="chip" href="/admin/alerts#cleared" aria-current="true">All</a>`)
	assert.Contains(t, body, `<a class="chip" href="/admin/alerts?kind=off_route#cleared">Off route</a>`)
	assert.Contains(t, body, "2 cleared alerts shown, page 1")
	assert.NotContains(t, body, "Next")
	assert.NotContains(t, body, `data-kind="off_route"`, "the open alerts' chips must not hide cleared rows")
}

func TestAlertsPage_ClearedOfOneKind(t *testing.T) {
	cleared := &fakeClearedAlerts{}
	mux := newClearedAlertsMux(t, cleared)

	body := getAdminPage(t, mux, "/admin/alerts?kind=off_route")

	assert.Equal(t, alertOffRoute, cleared.kind)
	assert.Contains(t, body, `<a class="chip" href="/admin/alerts?kind=off_route#cleared" aria-current="true">Off route</a>`)
	assert.Contains(t, body, `<a class="chip" href="/admin/alerts#cleared">All</a>`)
	assert.Contains(t, body, "No cleared alerts here")
}

func TestAlertsPage_ClearedPages(t *testing.T) {
	alerts := make([]clearedAlert, adminPageSize+1)
	for i := range alerts {
		alerts[i] = clearedAt(alertQuiet, "bus-7", alertT0, time.Minute)
	}
	cleared := &fakeClearedAlerts{alerts: alerts}
	mux := newClearedAlertsMux(t, cleared)

	body := getAdminPage(t, mux, "/admin/alerts?kind=quiet&page=2")

	assert.Equal(t, alertQuiet, cleared.kind)
	assert.Equal(t, adminPageSize, cleared.offset)
	assert.Len(t, clearedRows(body), adminPageSize, "the row past the page only says there is a next")
	assert.Contains(t, body, "50 cleared alerts shown, page 2")
	assert.Contains(t, body, `href="/admin/alerts?kind=quiet#cleared">`, "back to page 1, the filter kept")
	assert.Contains(t, body, `href="/admin/alerts?kind=quiet&amp;page=3#cleared">Next`)
}

func TestAlertsPage_NoClearedAlert(t *testing.T) {
	mux := newClearedAlertsMux(t, &fakeClearedAlerts{})

	body := getAdminPage(t, mux, "/admin/alerts")

	assert.Contains(t, body, "No alert has cleared yet")
	assert.NotContains(t, body, "cleared alerts shown")
}

func TestAlertsPage_UnknownKind(t *testing.T) {
	cleared := &fakeClearedAlerts{}
	mux := newClearedAlertsMux(t, cleared)

	w := requestAdminPage(t, mux, "/admin/alerts?kind=late")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "kind must be quiet, not_moving, off_route or not_started")
	assert.Zero(t, cleared.limit, "nothing is read for a kind there is not")
}

func TestAlertsPage_ClearedListError(t *testing.T) {
	captureWebhookLogs(t)
	mux := newClearedAlertsMux(t, &fakeClearedAlerts{err: errors.New("database down")})

	w := requestAdminPage(t, mux, "/admin/alerts")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "30", w.Header().Get("Refresh"), "a page left open keeps reloading through the outage")
}

func TestClearedAlertsURL(t *testing.T) {
	assert.Equal(t, "/admin/alerts#cleared", clearedAlertsURL("", 1))
	assert.Equal(t, "/admin/alerts#cleared", clearedAlertsURL("", 0), "page 0 is page 1")
	assert.Equal(t, "/admin/alerts?page=2#cleared", clearedAlertsURL("", 2))
	assert.Equal(t, "/admin/alerts?kind=not_moving&page=3#cleared", clearedAlertsURL(alertNotMoving, 3))
}

func TestNewClearedAlertRow_VehicleWithNoLabel(t *testing.T) {
	a := clearedAt(alertQuiet, "bus-7", alertT0, 3*time.Minute)
	a.VehicleLabel = ""

	row := newClearedAlertRow(a, nil, time.UTC, alertT0)

	assert.Equal(t, "bus-7", row.VehicleLabel, "the id stands in for a vehicle with no label")
	assert.Equal(t, "Gone quiet", row.Title)
	assert.Equal(t, "3 min", row.Lasted)
}

func TestAlertsPage_ClearedFromTheStore(t *testing.T) {
	store := newTestStore(t)
	trip := startAlertTrip(t, store)
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	for i, kind := range []string{alertQuiet, alertOffRoute, alertNotMoving, alertQuiet} {
		a := newAlert(kind, at.Add(time.Duration(i)*time.Minute), trip, at)
		id, err := store.RaiseAlert(context.Background(), a)
		require.NoError(t, err)
		require.NoError(t, store.ClearAlert(context.Background(), id, at.Add(time.Duration(10+i)*time.Minute)))
	}
	ui := newTestAdminUI(t)
	ui.clearedAlerts = store
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)

	all := clearedRows(getAdminPage(t, mux, "/admin/alerts"))
	quiet := clearedRows(getAdminPage(t, mux, "/admin/alerts?kind=quiet"))

	assert.Equal(t, [][2]string{{"bus-trip-1", "Gone quiet"}, {"bus-trip-1", "Not moving"}, {"bus-trip-1", "Off route"}, {"bus-trip-1", "Gone quiet"}}, all, "newest cleared first")
	assert.Equal(t, [][2]string{{"bus-trip-1", "Gone quiet"}, {"bus-trip-1", "Gone quiet"}}, quiet)
}
