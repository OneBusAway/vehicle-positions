package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// filteringUserStore is a fakeUserStore whose page of users honours the
// filter's role and active flag, as the store's query does.
type filteringUserStore struct {
	*fakeUserStore
}

func (f filteringUserStore) ListUsersPage(ctx context.Context, filter UserFilter) ([]UserResponse, error) {
	all, err := f.fakeUserStore.ListUsersPage(ctx, UserFilter{Limit: 1 << 20})
	if err != nil {
		return nil, err
	}
	var out []UserResponse
	for _, u := range all {
		if (filter.Role == "" || u.Role == filter.Role) && (!filter.ActiveOnly || u.Active) {
			out = append(out, u)
		}
	}
	return pageSlice(out, filter.Limit, filter.Offset), nil
}

var pickerOptionPattern = regexp.MustCompile(`<option value="(\d*)"( selected)?>([^<]*)</option>`)

// pickerOptions is the driver picker's options as value and label, with the
// selected one's value.
func pickerOptions(t *testing.T, body string) (options [][2]string, selected string) {
	t.Helper()
	start := strings.Index(body, `<select class="select" id="user" name="user"`)
	require.GreaterOrEqual(t, start, 0, "the page has a driver picker")
	end := strings.Index(body[start:], "</select>")
	require.Greater(t, end, 0)
	for _, m := range pickerOptionPattern.FindAllStringSubmatch(body[start:start+end], -1) {
		options = append(options, [2]string{m[1], m[3]})
		if m[2] != "" {
			selected = m[1]
		}
	}
	return options, selected
}

func pickerUsers() *fakeUserStore {
	return newFakeUserStore(
		UserResponse{ID: 7, Name: "Asha Rao", Email: "asha.rao@agency.example", Role: roleDriver, Active: true},
		UserResponse{ID: 8, Name: "Old Driver", Email: "old@agency.example", Role: roleDriver, Active: false},
		UserResponse{ID: 9, Name: "Ada Admin", Email: "ada@agency.example", Role: "admin", Active: true},
		UserResponse{ID: 10, Name: "aaron Bello", Email: "aaron@agency.example", Role: roleDriver, Active: true},
	)
}

// With no driver chosen, the page offers the active drivers, by name, and the
// sheet for anyone; the picker sends the same ?user= a driver's row links to.
func TestDriverSetupPage_PickerListsActiveDrivers(t *testing.T) {
	ui := newTestAdminUI(t)
	wireFakeUserStore(ui, pickerUsers(), newFakeAssignmentStore())
	ui.userPager = filteringUserStore{pickerUsers()}
	w := requestDriverSetup(t, ui, "https://tracker.example.org/admin/driver-setup", nil)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	options, selected := pickerOptions(t, body)
	assert.Equal(t, [][2]string{
		{"", "Anyone: no name on the sheet"},
		{"10", "aaron Bello (aaron@agency.example)"},
		{"7", "Asha Rao (asha.rao@agency.example)"},
	}, options, "only active drivers, by name whatever its case")
	assert.Equal(t, "", selected)
	assert.NotContains(t, body, "newest drivers", "every driver is listed, so the help names no cap")
	assert.Contains(t, body, `<label class="label" for="user">Driver</label>`)
	assert.Contains(t, body, `<form class="card setup-card" method="get" action="/admin/driver-setup">`,
		"the picker sends ?user=, the link a driver's row has")
}

// The chosen driver's sheet keeps them chosen, so changing the address keeps
// their sheet, and is the sheet their row links to.
func TestDriverSetupPage_PickerKeepsTheDriver(t *testing.T) {
	ui := newTestAdminUI(t)
	wireFakeUserStore(ui, pickerUsers(), newFakeAssignmentStore())
	ui.userPager = filteringUserStore{pickerUsers()}

	picked := requestDriverSetup(t, ui, "https://tracker.example.org/admin/driver-setup?user=7&server=https%3A%2F%2Ftracker.example.org", nil)
	row := requestDriverSetup(t, ui, "https://tracker.example.org/admin/driver-setup?user=7", nil)
	require.Equal(t, http.StatusOK, picked.Code)
	_, selected := pickerOptions(t, picked.Body.String())
	assert.Equal(t, "7", selected)
	assert.Equal(t, sheetOf(t, row.Body.String()), sheetOf(t, picked.Body.String()), "the picker leads to the sheet the row does")
	assert.Contains(t, picked.Body.String(), `<div class="sheet-for">For Asha Rao</div>`)

	// A user the picker does not list, reached from their row, stays chosen.
	admin := requestDriverSetup(t, ui, "https://tracker.example.org/admin/driver-setup?user=9", nil)
	require.Equal(t, http.StatusOK, admin.Code)
	options, selected := pickerOptions(t, admin.Body.String())
	assert.Equal(t, "9", selected)
	assert.Contains(t, options, [2]string{"9", "Ada Admin (ada@agency.example)"})
}

// With more active drivers than the picker holds, its help says how many it
// lists: the cap, not the cap plus a user the picker adds because ?user= named
// them.
func TestDriverSetupPage_PickerCapCount(t *testing.T) {
	users := make([]UserResponse, 0, maxSheetDrivers+2)
	for i := 1; i <= maxSheetDrivers+1; i++ {
		users = append(users, UserResponse{ID: int64(i), Name: fmt.Sprintf("Driver %04d", i), Email: fmt.Sprintf("d%d@agency.example", i), Role: roleDriver, Active: true})
	}
	adminID := int64(maxSheetDrivers + 2)
	users = append(users, UserResponse{ID: adminID, Name: "Ada Admin", Email: "ada@agency.example", Role: "admin", Active: true})
	store := newFakeUserStore(users...)
	ui := newTestAdminUI(t)
	wireFakeUserStore(ui, store, newFakeAssignmentStore())
	ui.userPager = filteringUserStore{store}
	help := fmt.Sprintf("This lists the %d newest drivers; for another, use Setup sheet on their row in Drivers and users.", maxSheetDrivers)

	for _, tc := range []struct {
		name     string
		query    string
		options  int
		selected string
	}{
		{"no user", "", maxSheetDrivers + 1, ""},
		{"a user the picker does not list", "?user=" + strconv.FormatInt(adminID, 10), maxSheetDrivers + 2, strconv.FormatInt(adminID, 10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := requestDriverSetup(t, ui, "https://tracker.example.org/admin/driver-setup"+tc.query, nil)
			require.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()
			options, selected := pickerOptions(t, body)
			assert.Len(t, options, tc.options, "Anyone, the newest drivers, and the user named")
			assert.Equal(t, tc.selected, selected)
			m := regexp.MustCompile(`<div class="help" id="user-help">([^<]*)</div>`).FindStringSubmatch(body)
			require.NotNil(t, m, "the picker has its help")
			assert.Contains(t, m[1], help)
		})
	}
}

// sheetOf is the page's one printed sheet.
func sheetOf(t *testing.T, body string) string {
	t.Helper()
	sheets := regexp.MustCompile(`(?s)<article class="sheet".*?</article>`).FindAllString(body, -1)
	require.Len(t, sheets, 1)
	return sheets[0]
}

// The picker reads the store's own filter: an admin and a deactivated driver
// are not offered.
func TestDriverSetupPage_PickerFromStore(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000000")
	driver, err := store.CreateUser(ctx, "Picker Driver "+suffix, "picker-driver-"+suffix+"@example.org", "a-long-password-1", roleDriver)
	require.NoError(t, err)
	gone, err := store.CreateUser(ctx, "Picker Gone "+suffix, "picker-gone-"+suffix+"@example.org", "a-long-password-1", roleDriver)
	require.NoError(t, err)
	require.NoError(t, store.SetUserActiveAndRevokeSessions(ctx, gone.ID, false))
	admin, err := store.CreateUser(ctx, "Picker Admin "+suffix, "picker-admin-"+suffix+"@example.org", "a-long-password-1", "admin")
	require.NoError(t, err)

	ui := newTestAdminUI(t)
	ui.userManager = store
	ui.userPager = store
	w := requestDriverSetup(t, ui, "https://tracker.example.org/admin/driver-setup", nil)
	require.Equal(t, http.StatusOK, w.Code)
	options, _ := pickerOptions(t, w.Body.String())
	values := make([]string, len(options))
	for i, o := range options {
		values[i] = o[0]
	}
	assert.Contains(t, values, strconv.FormatInt(driver.ID, 10))
	assert.NotContains(t, values, strconv.FormatInt(gone.ID, 10), "a deactivated driver is not offered")
	assert.NotContains(t, values, strconv.FormatInt(admin.ID, 10), "an admin is not offered")
}

// A three-driver import, two of them new: the result can print one sheet per
// imported driver, each with its driver's name and email, and a new driver's
// one-time password on theirs alone.
func TestImportConfirm_PrintAllSheets(t *testing.T) {
	imp := &fakeImporter{planned: []importOutcome{
		{Status: importNew},
		{Status: importUpdate, Changes: []string{"adds 102"}},
		{Status: importNew},
		{Status: importUnchanged},
	}}
	ui := newImportTestUI(t, imp)
	file := driversHeader + "ana@example.org,Ana,101\nbo@example.org,Bo,102\ncy@example.org,Cy,\ndee@example.org,Dee,\n"
	w := confirmImport(t, ui, "drivers", signedForm("drivers", file, time.Now()))
	require.Equal(t, http.StatusOK, w.Code, pageText(w))
	body := w.Body.String()

	passwords := tablePasswords(t, body)
	require.Len(t, passwords, 2)
	assert.True(t, strings.Contains(body, "Print all sheets"), "the result offers to print every sheet")

	start := strings.Index(body, `<div class="sheets">`)
	require.GreaterOrEqual(t, start, 0, "the result holds the sheets to print")
	result := strings.Index(body, `<div class="no-print">`)
	require.GreaterOrEqual(t, result, 0, "printing the page prints the sheets, not the result")
	assert.Less(t, result, strings.Index(body, `<table class="table cards">`))
	assert.Greater(t, start, strings.Index(body, `</table>`), "the sheets are outside the result")
	sheets := regexp.MustCompile(`(?s)<article class="sheet".*?</article>`).FindAllString(body[start:], -1)
	require.Len(t, sheets, 3, "one sheet per imported driver, not the unchanged one")
	assert.Equal(t, 3, strings.Count(body[start:], `<div style="break-after:page">`), "one sheet per page")
	for i, want := range []struct{ name, email, password string }{
		{"Ana", "ana@example.org", passwords[0]},
		{"Bo", "bo@example.org", ""},
		{"Cy", "cy@example.org", passwords[1]},
	} {
		assert.Contains(t, sheets[i], `<div class="sheet-for">For `+want.name+`</div>`)
		assert.Contains(t, sheets[i], "<b>"+want.email+"</b>")
		assert.Equal(t, 1, strings.Count(sheets[i], exampleQRHost(t, body)), "each sheet has the server")
		if want.password == "" {
			assert.Contains(t, sheets[i], "and your password.", "an updated driver keeps the password they have")
			for _, pw := range passwords {
				assert.NotContains(t, sheets[i], pw)
			}
			continue
		}
		assert.Contains(t, sheets[i], want.password)
		for _, pw := range passwords {
			if pw != want.password {
				assert.NotContains(t, sheets[i], pw, "a sheet carries its own driver's password only")
			}
		}
	}
	assert.NotContains(t, body[start:], "dee@example.org", "an unchanged driver gets no sheet")
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

// Sheets are hidden on screen and shown in print, where the result around
// them is not.
func TestAdminCSS_PrintAllSheets(t *testing.T) {
	css, err := fs.ReadFile(files, "web/static/css/admin.css")
	require.NoError(t, err)
	assert.Contains(t, string(css), ".sheets{display:none}")
	prints := regexp.MustCompile(`@media print\{(?:[^{}]*\{[^}]*\})*`).FindAllString(string(css), -1)
	var print string
	for _, p := range prints {
		print += p
	}
	assert.Contains(t, print, ".sheets{display:block}")
	assert.Contains(t, print, ".no-print")
}

// A vehicles import has no sheets to print.
func TestImportConfirm_VehiclesHaveNoSheets(t *testing.T) {
	ui := newImportTestUI(t, &fakeImporter{})
	w := confirmImport(t, ui, "vehicles", signedForm("vehicles", vehiclesHeader+"bus-1,Bus 1,\n", time.Now()))
	require.Equal(t, http.StatusOK, w.Code)
	assert.False(t, strings.Contains(w.Body.String(), "Print all sheets"))
	assert.False(t, strings.Contains(w.Body.String(), `class="sheets"`))
}

// A new driver's password is on the import's result, in its table and on that
// driver's sheet, and nowhere else: not on the driver's own setup sheet, not
// in the picker.
func TestImportConfirm_PasswordOnlyOnTheResult(t *testing.T) {
	imp := &fakeImporter{}
	ui := newImportTestUI(t, imp)
	file := driversHeader + "ana@example.org,Ana,\n"
	w := confirmImport(t, ui, "drivers", signedForm("drivers", file, time.Now()))
	require.Equal(t, http.StatusOK, w.Code, pageText(w))
	passwords := tablePasswords(t, w.Body.String())
	require.Len(t, passwords, 1)
	pw := passwords[0]
	assert.Equal(t, 2, strings.Count(w.Body.String(), pw), "the table and the driver's sheet")

	users := newFakeUserStore(UserResponse{ID: 100, Name: "Ana", Email: "ana@example.org", Role: roleDriver, Active: true})
	wireFakeUserStore(ui, users, newFakeAssignmentStore())
	ui.userPager = filteringUserStore{users}
	for _, target := range []string{"/admin/driver-setup", "/admin/driver-setup?user=100"} {
		page := requestDriverSetup(t, ui, "https://tracker.example.org"+target, nil)
		require.Equal(t, http.StatusOK, page.Code)
		assert.NotContains(t, page.Body.String(), pw, target)
		assert.NotContains(t, page.Body.String(), `class="pw"`, target)
	}
}

var tablePasswordPattern = regexp.MustCompile(`<span id="pw-\d+" class="pw">([^<]*)</span>`)

// tablePasswords is the passwords in the result table, in row order.
func tablePasswords(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, m := range tablePasswordPattern.FindAllStringSubmatch(body, -1) {
		require.Regexp(t, driverPasswordPattern, m[1])
		out = append(out, m[1])
	}
	return out
}

// exampleQRHost is the host the result's sheets name under their code.
func exampleQRHost(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`<div class="sheet-host">([^<]*)</div>`).FindStringSubmatch(body)
	require.NotNil(t, m, "a sheet names the server under its code")
	return `<div class="sheet-host">` + m[1] + `</div>`
}
