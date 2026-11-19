package main

import (
	"cmp"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// setupLinkPrefix starts every setup link the driver apps take
// (ServerSetupLink in both apps): host setup, with server as the only query
// item.
const setupLinkPrefix = "obavehicletracker://setup?server="

// setupLink is the link that sets server on a driver's phone. Every byte but
// a letter or a digit is percent-encoded, as both apps' ServerSetupLink tests
// build their links. url.QueryEscape would turn a space into "+", which the
// apps read as a plus.
func setupLink(server string) string {
	var b strings.Builder
	b.WriteString(setupLinkPrefix)
	for i := 0; i < len(server); i++ {
		c := server[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// requestServerAddress is this server's address as the request for the page
// shows it: https when the request came over TLS, or through a trusted proxy
// that says so in X-Forwarded-Proto (requestIsSecure), and the Host it was
// sent to, which the nginx site in docs/deployment.md passes on.
func requestServerAddress(r *http.Request, trustProxy bool) string {
	scheme := "http"
	if requestIsSecure(r, trustProxy) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// driverSetup is what the Driver setup page shows for one server address.
type driverSetup struct {
	Link string
	// Host is what the app names when it asks the driver "Use <host>?".
	Host string
	QR   template.HTML
	// Insecure is a plain http address. The apps take one only from a
	// development build, so a sheet printed with it will not work on drivers'
	// phones.
	Insecure bool
}

// newDriverSetup checks server against the rules the apps hold a setup link's
// address to (docs/deployment.md, section 13), and draws its link as a QR code.
// The error is shown to the admin as it is, like the user form's.
func newDriverSetup(server string) (driverSetup, error) {
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return driverSetup{}, errors.New("type the server's full address, such as https://tracker.example.org")
	}
	if u.User != nil {
		// https://tracker.example.org@evil.example goes to evil.example, so
		// the apps refuse any address with user info.
		return driverSetup{}, errors.New("leave the user name and password out of the address: the apps refuse an address that has them")
	}
	link := setupLink(server)
	qr, err := qrSVG(link)
	if err != nil {
		// The link is never empty, so the one error left is a link too long
		// for any QR code.
		return driverSetup{}, errors.New("the address is too long to put in a QR code")
	}
	return driverSetup{
		Link:     link,
		Host:     u.Hostname(),
		QR:       qr,
		Insecure: u.Scheme == "http",
	}, nil
}

// setupSheet is one sheet to hand a driver (the setup-sheet template): the
// setup it carries, the server it names, and when it is for one driver, their
// name and email. Password is set only on an import's result, for a driver the
// import has just made: that page is the only place the password exists, and
// the sheet is how it reaches the driver on paper. It is never stored, and no
// other page puts it on a sheet.
type setupSheet struct {
	Setup    driverSetup
	Server   string
	Name     string
	Email    string
	Password string
}

// maxSheetDrivers is how many drivers the Driver setup page's picker lists,
// the newest first; a driver beyond them is reached from their row.
const maxSheetDrivers = 1000

// importSheets is a sheet for each driver an import has just added or changed,
// for its result page to print all at once. Each row's cells are the drivers
// file's columns: email, then name. None when this server's address can't
// make a sheet.
func importSheets(r *http.Request, trustProxy bool, done []importRowView) []setupSheet {
	server := requestServerAddress(r, trustProxy)
	setup, err := newDriverSetup(server)
	if err != nil {
		return nil
	}
	sheets := make([]setupSheet, len(done))
	for i, v := range done {
		sheets[i] = setupSheet{Setup: setup, Server: server, Email: v.Cells[0], Name: v.Cells[1], Password: v.Password}
	}
	return sheets
}

// qrLabel names the QR code for a screen reader. It is fixed text, so qrSVG
// may write it into the SVG.
const qrLabel = "QR code of the setup link"

// qrSVG draws content as a QR code: black modules on a white square, with the
// four-module margin scanners need around the code. The viewBox counts
// modules, so the code stays sharp at any size on screen or paper. Medium
// error correction lets a creased or smudged sheet still scan.
//
// The SVG is built only from numbers and fixed text, so it is safe to put in
// the page unescaped.
func qrSVG(content string) (template.HTML, error) {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", err
	}
	bitmap := q.Bitmap()
	size := len(bitmap)

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="%s">`, size, size, qrLabel)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, size, size)
	// One subpath per run of dark modules in a row.
	for y, row := range bitmap {
		for x := 0; x < len(row); {
			if !row[x] {
				x++
				continue
			}
			run := 1
			for x+run < len(row) && row[x+run] {
				run++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", x, y, run, run)
			x += run
		}
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String()), nil
}

// driverSetupPage renders the Driver setup page: this server's address, the
// setup link that fills it in on a driver's phone, a QR code of the link, and
// a sheet to print for a new driver. ?server= replaces the address worked out
// from the request. ?user= prints that user's name and email on the sheet;
// never a password, and the link holds only the server, since that is all the
// apps accept. A picker of the active drivers sends ?user=, the link a
// driver's row in Drivers and users has.
func (ui *adminUI) driverSetupPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Title": "Driver setup",
		"Page":  "driver-setup",
	}

	if raw := r.URL.Query().Get("user"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		user, err := ui.userManager.GetUser(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				http.NotFound(w, r)
				return
			}
			slog.Error("driver setup: get user", "user_id", id, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		data["User"] = user
	}

	drivers, err := ui.userPager.ListUsersPage(r.Context(), UserFilter{Role: roleDriver, ActiveOnly: true, Limit: maxSheetDrivers + 1})
	if err != nil {
		slog.Error("driver setup: list drivers", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if len(drivers) > maxSheetDrivers {
		drivers = drivers[:maxSheetDrivers]
		data["MoreDrivers"] = maxSheetDrivers
	}
	slices.SortFunc(drivers, func(a, b UserResponse) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.Email, b.Email))
	})
	// A user the picker doesn't list, reached from their row, stays chosen,
	// so changing the address keeps their sheet.
	if user, ok := data["User"].(*UserResponse); ok && !slices.ContainsFunc(drivers, func(d UserResponse) bool { return d.ID == user.ID }) {
		drivers = append([]UserResponse{*user}, drivers...)
	}
	data["Drivers"] = drivers

	server := strings.TrimSpace(r.URL.Query().Get("server"))
	if server == "" {
		server = requestServerAddress(r, ui.cfg.trustProxy)
	}
	data["Server"] = server

	setup, err := newDriverSetup(server)
	if err != nil {
		data["Error"] = err.Error()
		ui.renderAdmin(w, r, http.StatusUnprocessableEntity, "driver_setup.html", data)
		return
	}
	data["Setup"] = setup
	sheet := setupSheet{Setup: setup, Server: server}
	if user, ok := data["User"].(*UserResponse); ok {
		sheet.Name, sheet.Email = user.Name, user.Email
	}
	data["Sheet"] = sheet
	ui.renderAdmin(w, r, http.StatusOK, "driver_setup.html", data)
}
