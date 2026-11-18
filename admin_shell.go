package main

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
)

// navItem is one page in the admin menu.
type navItem struct {
	// Page is the .Page value of the views this item is marked open on.
	Page  string
	Label string
	Href  string
	// Icon is a symbol id in web/static/icons.svg.
	Icon string
	// Alerts marks the item that carries the count of open alerts.
	Alerts bool
}

type navGroup struct {
	Title string
	Items []navItem
}

// adminNav is the menu. The rail and the More page both draw it, so a page
// added here is in both.
var adminNav = []navGroup{
	{Title: "Live", Items: []navItem{
		{Page: "map", Label: "Live map", Href: "/admin/map", Icon: "map"},
		{Page: "alerts", Label: "Alerts", Href: "/admin/alerts", Icon: "notifications", Alerts: true},
		{Page: "trips", Label: "Trips", Href: "/admin/trips", Icon: "route"},
	}},
	{Title: "Insights", Items: []navItem{
		{Page: "dashboard", Label: "Overview", Href: "/admin/dashboard", Icon: "space_dashboard"},
		{Page: "reports", Label: "Analytics", Href: "/admin/reports", Icon: "monitoring"},
		{Page: "rider", Label: "Rider mode", Href: "/admin/rider", Icon: "hail"},
		{Page: "health", Label: "Feed health", Href: "/admin/health", Icon: "sensors"},
	}},
	{Title: "Manage", Items: []navItem{
		{Page: "vehicles", Label: "Vehicles", Href: "/admin/vehicles", Icon: "directions_bus"},
		{Page: "users", Label: "Drivers and users", Href: "/admin/users", Icon: "group"},
		{Page: "driver-setup", Label: "Driver setup", Href: "/admin/driver-setup", Icon: "qr_code_2"},
		{Page: "api-keys", Label: "API keys", Href: "/admin/api-keys", Icon: "key"},
	}},
}

// shellAccount is the signed-in admin as the account menu shows them.
type shellAccount struct {
	// Name is empty when the user's row could not be read; the menu then
	// shows the email alone.
	Name     string
	Email    string
	Initials string
}

// shellData is what every admin page's rail and top bar are drawn from.
type shellData struct {
	Nav        []navGroup
	AlertCount int
	fleetCounts
	Account shellAccount
}

// AlertsLabel names the links to the Alerts page for a screen reader, which
// would otherwise read the count as part of the word.
func (s shellData) AlertsLabel() string {
	if s.AlertCount == 0 {
		return "Alerts"
	}
	return "Alerts, " + strconv.Itoa(s.AlertCount) + " open"
}

// fleetCounts is the pulse in the top bar: Reporting is the vehicles heard
// from within the staleness threshold that no open alert calls quiet, and
// Quiet the vehicles on a running trip that have gone quiet.
type fleetCounts struct {
	Reporting int
	Quiet     int
}

// fleetNow counts the fleet from the open alerts and the tracker. A vehicle
// that has gone quiet stays in the tracker until the staleness threshold
// passes, so it is taken out of the reporting count by its id rather than
// counted on both sides.
func (ui *adminUI) fleetNow(alerts []alertEntry) fleetCounts {
	quiet := make(map[string]struct{})
	for _, a := range alerts {
		if a.Kind == alertQuiet {
			quiet[a.Trip.VehicleID] = struct{}{}
		}
	}
	reporting := 0
	for _, v := range ui.tracker.ActiveVehicles() {
		if _, isQuiet := quiet[v.VehicleID]; !isQuiet {
			reporting++
		}
	}
	return fleetCounts{Reporting: reporting, Quiet: len(quiet)}
}

// shell works out the shell's data from the open alerts and the tracker.
func (ui *adminUI) shell(r *http.Request, alerts []alertEntry) shellData {
	return shellData{
		Nav:         adminNav,
		AlertCount:  len(alerts),
		fleetCounts: ui.fleetNow(alerts),
		Account:     ui.account(r),
	}
}

// account is the signed-in admin for the account menu. The session carries
// the email; the name is read from the user's row. A menu label is not worth
// failing a page over, so a failed read is logged and the email shown alone.
func (ui *adminUI) account(r *http.Request) shellAccount {
	claims, _ := r.Context().Value(claimsKey).(jwt.MapClaims)
	email, _ := claims["email"].(string)
	acct := shellAccount{Email: email}

	sub, _ := claims.GetSubject()
	id, err := strconv.ParseInt(sub, 10, 64)
	if err != nil {
		slog.Warn("account menu: session sub is not a user id", "sub", sub, "error", err)
	} else if user, err := ui.userManager.GetUser(r.Context(), id); err != nil {
		slog.Warn("account menu: read user", "user_id", id, "error", err)
	} else {
		acct.Name = user.Name
		acct.Email = user.Email
	}
	acct.Initials = initials(acct.Name, acct.Email)
	return acct
}

// initials is the one or two letters on the account button: the first
// letters of the first two words of name, or of email when there is no name.
func initials(name, email string) string {
	words := strings.Fields(name)
	if len(words) == 0 {
		words = strings.Fields(email)
	}
	if len(words) > 2 {
		words = words[:2]
	}
	var b strings.Builder
	for _, w := range words {
		for _, r := range w {
			b.WriteRune(unicode.ToUpper(r))
			break
		}
	}
	return b.String()
}

// listAlertsForShell is the open alerts for a page that does not show them
// itself. A badge is not worth failing a page over, so an error is logged
// and the shell drawn with no alerts.
func (ui *adminUI) listAlertsForShell(r *http.Request) []alertEntry {
	alerts, err := ui.alerts.list(r.Context(), time.Now())
	if err != nil {
		slog.Error("sidebar: list alerts", "error", err)
		return nil
	}
	return alerts
}

// morePage is the whole menu as a page, which is what the phone layout's
// More tab opens, so the menu works there without JavaScript.
func (ui *adminUI) morePage(w http.ResponseWriter, r *http.Request) {
	ui.renderAdmin(w, r, http.StatusOK, "more.html", map[string]interface{}{
		"Title": "Menu",
		"Page":  "more",
	})
}
