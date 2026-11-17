package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// noopStore's imports plan and write nothing; the wiring tests never get as
// far as them with a file.
func (n *noopStore) PlanVehicleImport(_ context.Context, rows []vehicleImportRow) ([]importOutcome, error) {
	return make([]importOutcome, len(rows)), nil
}
func (n *noopStore) ImportVehicles(_ context.Context, _ importPreview, rows []vehicleImportRow) ([]importOutcome, error) {
	return make([]importOutcome, len(rows)), nil
}
func (n *noopStore) PlanDriverImport(_ context.Context, rows []driverImportRow) ([]importOutcome, error) {
	return make([]importOutcome, len(rows)), nil
}
func (n *noopStore) ImportDrivers(_ context.Context, _ importPreview, rows []driverImportRow, _ func() string) ([]importOutcome, error) {
	return make([]importOutcome, len(rows)), nil
}

// fakeImporter records the rows and the preview each import call gets. A
// plan says every row is new unless planned is set; an import returns its
// plan, with a password and an account id for each new driver, unless
// imported is set.
type fakeImporter struct {
	planned  []importOutcome
	imported []importOutcome
	err      error
	delay    time.Duration

	vehiclePlans   [][]vehicleImportRow
	vehicleImports [][]vehicleImportRow
	driverPlans    [][]driverImportRow
	driverImports  [][]driverImportRow
	previews       []importPreview
}

func (f *fakeImporter) plan(n int) []importOutcome {
	if f.planned != nil {
		return f.planned
	}
	out := make([]importOutcome, n)
	for i := range out {
		out[i] = importOutcome{Status: importNew}
	}
	return out
}

func (f *fakeImporter) PlanVehicleImport(_ context.Context, rows []vehicleImportRow) ([]importOutcome, error) {
	f.vehiclePlans = append(f.vehiclePlans, rows)
	return f.plan(len(rows)), f.err
}

func (f *fakeImporter) ImportVehicles(_ context.Context, preview importPreview, rows []vehicleImportRow) ([]importOutcome, error) {
	f.vehicleImports = append(f.vehicleImports, rows)
	f.previews = append(f.previews, preview)
	time.Sleep(f.delay) // stands in for a slow import
	if f.imported != nil {
		return f.imported, f.err
	}
	return f.plan(len(rows)), f.err
}

func (f *fakeImporter) PlanDriverImport(_ context.Context, rows []driverImportRow) ([]importOutcome, error) {
	f.driverPlans = append(f.driverPlans, rows)
	return f.plan(len(rows)), f.err
}

func (f *fakeImporter) ImportDrivers(_ context.Context, preview importPreview, rows []driverImportRow, newPassword func() string) ([]importOutcome, error) {
	f.driverImports = append(f.driverImports, rows)
	f.previews = append(f.previews, preview)
	if f.imported != nil {
		return f.imported, f.err
	}
	out := f.plan(len(rows))
	for i := range out {
		out[i].UserID = int64(100 + i)
		if out[i].Status == importNew {
			out[i].Password = newPassword()
		}
	}
	return out, f.err
}

func newImportTestUI(t *testing.T, imp CSVImporter) *adminUI {
	t.Helper()
	ui := newTestAdminUI(t)
	ui.importer = imp
	return ui
}

// sendImport sends a request to the admin UI's mux with an admin session.
func sendImport(t *testing.T, ui *adminUI, method, target, contentType string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	req := httptest.NewRequest(method, target, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.AddCookie(cookieFor(t, "admin"))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func multipartFile(t *testing.T, file []byte) (string, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "import.csv")
	require.NoError(t, err)
	_, err = fw.Write(file)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	return mw.FormDataContentType(), &buf
}

func uploadImport(t *testing.T, ui *adminUI, kind, file string) *httptest.ResponseRecorder {
	t.Helper()
	contentType, body := multipartFile(t, []byte(file))
	return sendImport(t, ui, http.MethodPost, "/admin/import/"+kind, contentType, body)
}

var hiddenInputPattern = regexp.MustCompile(`<input type="hidden" name="(data|issued|sig)" value="([^"]*)">`)

// confirmForm is the preview page's confirm form, as the browser sends it.
func confirmForm(t *testing.T, page string) url.Values {
	t.Helper()
	form := url.Values{}
	for _, m := range hiddenInputPattern.FindAllStringSubmatch(page, -1) {
		form.Set(m[1], html.UnescapeString(m[2]))
	}
	require.Len(t, form, 3, "the preview must carry the file to the confirm step")
	return form
}

func confirmImport(t *testing.T, ui *adminUI, kind string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return sendImport(t, ui, http.MethodPost, "/admin/import/"+kind+"/confirm", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
}

// signedForm is a confirm form carrying file, signed as a preview would sign it.
func signedForm(kind, file string, at time.Time) url.Values {
	tok := signImport(testSecret, kind, at, []byte(file))
	return url.Values{"data": {tok.Data}, "issued": {tok.Issued}, "sig": {tok.Sig}}
}

// pageText is the page with its HTML entities decoded, for matching messages.
func pageText(w *httptest.ResponseRecorder) string {
	return html.UnescapeString(w.Body.String())
}

const vehiclesHeader = "id,label,agency_tag\n"
const driversHeader = "email,name,vehicle_ids\n"

func TestImportRoutes_RequireAdminSession(t *testing.T) {
	ui := newImportTestUI(t, &fakeImporter{})
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/admin/import/vehicles"},
		{http.MethodGet, "/admin/import/drivers/template.csv"},
		{http.MethodPost, "/admin/import/vehicles"},
		{http.MethodPost, "/admin/import/drivers/confirm"},
	} {
		for _, cookie := range []*http.Cookie{nil, cookieFor(t, "driver")} {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if cookie != nil {
				req.AddCookie(cookie)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusSeeOther, w.Code, "%s %s", tc.method, tc.path)
			assert.Equal(t, loginLocation(tc.method, tc.path), w.Header().Get("Location"))
		}
	}
}

// The import's POSTs are refused cross-origin by the CSRF protection that
// wraps every route, even with an admin session.
func TestImportRoutes_CrossOriginPostRefused(t *testing.T) {
	h := newTestHandler(t, true)
	for _, path := range []string{"/admin/import/vehicles", "/admin/import/vehicles/confirm"} {
		for _, site := range []string{"cross-site", "same-origin"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
			req.Header.Set("Sec-Fetch-Site", site)
			req.AddCookie(cookieFor(t, "admin"))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if site == "cross-site" {
				assert.Equal(t, http.StatusForbidden, w.Code, path)
			} else {
				// Reaches the handler, which refuses the empty form.
				assert.Equal(t, http.StatusBadRequest, w.Code, path)
			}
		}
	}
}

func TestImportRoutes_UnknownKind(t *testing.T) {
	ui := newImportTestUI(t, &fakeImporter{})
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/admin/import/admins"},
		{http.MethodGet, "/admin/import/admins/template.csv"},
		{http.MethodPost, "/admin/import/admins"},
		{http.MethodPost, "/admin/import/admins/confirm"},
	} {
		w := sendImport(t, ui, tc.method, tc.path, "", nil)
		assert.Equal(t, http.StatusNotFound, w.Code, "%s %s", tc.method, tc.path)
	}
}

func TestImportPage(t *testing.T) {
	ui := newImportTestUI(t, &fakeImporter{})
	for _, kind := range []string{"vehicles", "drivers"} {
		w := sendImport(t, ui, http.MethodGet, "/admin/import/"+kind, "", nil)
		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, `action="/admin/import/`+kind+`" enctype="multipart/form-data"`)
		assert.Contains(t, body, `href="/admin/import/`+kind+`/template.csv"`)
		for _, col := range importKinds[kind].Columns {
			assert.Contains(t, body, ">"+col.Name+"</span>")
		}
	}
}

func TestImportListPages_LinkToImport(t *testing.T) {
	ui := newImportTestUI(t, &fakeImporter{})
	for page, kind := range map[string]string{"/admin/vehicles": "vehicles", "/admin/users": "drivers"} {
		w := sendImport(t, ui, http.MethodGet, page, "", nil)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `<a href="/admin/import/`+kind+`"`, page)
	}
}

func TestImportTemplate(t *testing.T) {
	ui := newImportTestUI(t, &fakeImporter{})
	for kind, want := range map[string]string{
		"vehicles": "id,label,agency_tag\r\n",
		"drivers":  "email,name,vehicle_ids\r\n",
	} {
		w := sendImport(t, ui, http.MethodGet, "/admin/import/"+kind+"/template.csv", "", nil)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "text/csv; charset=utf-8", w.Header().Get("Content-Type"))
		assert.Equal(t, `attachment; filename="`+kind+`-template.csv"`, w.Header().Get("Content-Disposition"))
		assert.Equal(t, want, w.Body.String())

		// The template's header is one the upload accepts.
		_, err := readImportFile(w.Body.Bytes(), importKinds[kind].columnNames())
		require.Error(t, err)
		assert.Equal(t, "the file has no rows under its header", err.Error())
	}
}

func TestImportUpload_VehicleRowErrors(t *testing.T) {
	cases := []struct {
		name, row, want string
	}{
		{"id missing", ",Bus 1,north", "vehicle id is required"},
		{"id too long", strings.Repeat("a", 51) + ",Bus 1,", "vehicle id must be at most 50 characters"},
		{"id characters", "bus 1/2,Bus 1,", "vehicle id must contain only alphanumeric characters, dots, hyphens, and underscores"},
		{"label too long", "bus-1," + strings.Repeat("l", 256) + ",", "label must be at most 255 characters"},
		{"agency tag too long", "bus-1,Bus 1," + strings.Repeat("t", 256), "agency_tag must be at most 255 characters"},
		{"extra cell", "bus-1,Bus 1,north,x", "this line has 4 cells and the header has 3"},
		{"missing cell", "bus-1,Bus 1", "this line has 2 cells and the header has 3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imp := &fakeImporter{}
			ui := newImportTestUI(t, imp)
			w := uploadImport(t, ui, "vehicles", vehiclesHeader+"ok-1,Fine,\n"+tc.row+"\n")
			require.Equal(t, http.StatusUnprocessableEntity, w.Code)
			body := pageText(w)
			assert.Contains(t, body, tc.want)
			assert.Contains(t, body, "1 new, 0 to update, 0 unchanged, 1 with errors")
			assert.NotContains(t, body, `name="sig"`, "a file with an error can't be confirmed")
			require.Len(t, imp.vehiclePlans, 1)
			assert.Equal(t, []vehicleImportRow{{Line: 2, ID: "ok-1", Label: "Fine"}}, imp.vehiclePlans[0], "only the rows that pass are planned")
		})
	}
}

func TestImportUpload_VehicleLimitsAccepted(t *testing.T) {
	imp := &fakeImporter{}
	ui := newImportTestUI(t, imp)
	id, label, tag := strings.Repeat("i", 50), strings.Repeat("l", 255), strings.Repeat("t", 255)
	w := uploadImport(t, ui, "vehicles", vehiclesHeader+id+","+label+","+tag+"\n")
	require.Equal(t, http.StatusOK, w.Code, pageText(w))
	require.Len(t, imp.vehiclePlans, 1)
	assert.Equal(t, []vehicleImportRow{{Line: 2, ID: id, Label: label, AgencyTag: tag}}, imp.vehiclePlans[0])
}

func TestImportUpload_DriverRowErrors(t *testing.T) {
	cases := []struct {
		name, row, want string
	}{
		{"name missing", "ana@example.org,,", "name is required"},
		{"email missing", ",Ana,", "email is required"},
		{"email not valid", "ana.example.org,Ana,", "email is not valid"},
		{"email with a display name", "Ana <ana@example.org>,Ana,", "email is not valid"},
		{"vehicle id not valid", "ana@example.org,Ana,101 10/2", "10/2: vehicle id must contain only alphanumeric characters, dots, hyphens, and underscores"},
		{"vehicle id too long", "ana@example.org,Ana," + strings.Repeat("v", 51), strings.Repeat("v", 51) + ": vehicle id must be at most 50 characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imp := &fakeImporter{}
			ui := newImportTestUI(t, imp)
			w := uploadImport(t, ui, "drivers", driversHeader+tc.row+"\n")
			require.Equal(t, http.StatusUnprocessableEntity, w.Code)
			assert.Contains(t, pageText(w), tc.want)
			require.Len(t, imp.driverPlans, 1)
			assert.Empty(t, imp.driverPlans[0])
		})
	}
}

func TestImportUpload_DriverRowsNormalized(t *testing.T) {
	imp := &fakeImporter{}
	ui := newImportTestUI(t, imp)
	w := uploadImport(t, ui, "drivers", driversHeader+" Ana@Example.ORG ,  Ana Pérez ,  101   102 101 \nbo@example.org,Bo,\n")
	require.Equal(t, http.StatusOK, w.Code, pageText(w))
	require.Len(t, imp.driverPlans, 1)
	assert.Equal(t, []driverImportRow{
		{Line: 2, Email: "ana@example.org", Name: "Ana Pérez", VehicleIDs: []string{"101", "102"}},
		{Line: 3, Email: "bo@example.org", Name: "Bo"},
	}, imp.driverPlans[0])
	assert.Contains(t, pageText(w), "ana@example.org")
	assert.NotContains(t, pageText(w), "Ana@Example.ORG")
}

func TestImportUpload_DuplicateRows(t *testing.T) {
	t.Run("vehicles", func(t *testing.T) {
		imp := &fakeImporter{}
		ui := newImportTestUI(t, imp)
		w := uploadImport(t, ui, "vehicles", vehiclesHeader+"bus-1,A,\nBus-1,B,\nbus-2,C,\nbus-1,D,\n")
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Contains(t, pageText(w), "vehicle id is already on line 2")
		assert.Contains(t, pageText(w), "3 new, 0 to update, 0 unchanged, 1 with errors", "ids differing in case are different vehicles")
		require.Len(t, imp.vehiclePlans, 1)
		assert.Len(t, imp.vehiclePlans[0], 3)
	})
	t.Run("drivers, in any case", func(t *testing.T) {
		imp := &fakeImporter{}
		ui := newImportTestUI(t, imp)
		w := uploadImport(t, ui, "drivers", driversHeader+"ana@example.org,Ana,\nbo@example.org,Bo,\nANA@example.org,Ana B,\n")
		require.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Contains(t, pageText(w), "email is already on line 2")
		require.Len(t, imp.driverPlans, 1)
		assert.Len(t, imp.driverPlans[0], 2)
	})
}

// fileOfSize is a valid vehicles file of exactly n bytes: its one row's last
// cell is padded with spaces, which the import trims.
func fileOfSize(t *testing.T, n int) string {
	t.Helper()
	start := vehiclesHeader + "bus-1,Bus 1,"
	require.Less(t, len(start)+1, n)
	return start + strings.Repeat(" ", n-len(start)-1) + "\n"
}

func TestImportUpload_SizeCap(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
		want int
	}{
		{"one under", maxImportBytes - 1, http.StatusOK},
		{"at the cap", maxImportBytes, http.StatusOK},
		{"one over", maxImportBytes + 1, http.StatusRequestEntityTooLarge},
		{"over the whole form's cap", maxImportBytes + importFormOverhead, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imp := &fakeImporter{}
			ui := newImportTestUI(t, imp)
			file := fileOfSize(t, tc.size)
			require.Len(t, file, tc.size)
			w := uploadImport(t, ui, "vehicles", file)
			require.Equal(t, tc.want, w.Code)
			if tc.want == http.StatusOK {
				require.Len(t, imp.vehiclePlans, 1)
				assert.Equal(t, []vehicleImportRow{{Line: 2, ID: "bus-1", Label: "Bus 1"}}, imp.vehiclePlans[0])
				return
			}
			assert.Contains(t, pageText(w), "the file is larger than 256 KB: split it into smaller files")
			assert.Empty(t, imp.vehiclePlans)
		})
	}
}

func vehicleRowsFile(n int) string {
	var b strings.Builder
	b.WriteString(vehiclesHeader)
	for i := range n {
		fmt.Fprintf(&b, "bus-%d,Bus %d,\n", i, i)
	}
	return b.String()
}

func TestImportUpload_RowCap(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows int
		want int
	}{
		{"one under", maxImportRows - 1, http.StatusOK},
		{"at the cap", maxImportRows, http.StatusOK},
		{"one over", maxImportRows + 1, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			imp := &fakeImporter{}
			ui := newImportTestUI(t, imp)
			w := uploadImport(t, ui, "vehicles", vehicleRowsFile(tc.rows))
			require.Equal(t, tc.want, w.Code)
			if tc.want == http.StatusOK {
				require.Len(t, imp.vehiclePlans, 1)
				assert.Len(t, imp.vehiclePlans[0], tc.rows)
				return
			}
			assert.Contains(t, pageText(w), "the file has more than 200 rows: split it into files of 200 rows or fewer")
			assert.Empty(t, imp.vehiclePlans)
		})
	}
	t.Run("blank lines don't count", func(t *testing.T) {
		imp := &fakeImporter{}
		ui := newImportTestUI(t, imp)
		w := uploadImport(t, ui, "vehicles", vehicleRowsFile(maxImportRows)+",,\n , ,\n\n")
		require.Equal(t, http.StatusOK, w.Code)
		require.Len(t, imp.vehiclePlans, 1)
		assert.Len(t, imp.vehiclePlans[0], maxImportRows)
	})
}

func TestImportUpload_ByteOrderMarkAndCRLF(t *testing.T) {
	imp := &fakeImporter{}
	ui := newImportTestUI(t, imp)
	w := uploadImport(t, ui, "vehicles", "\xef\xbb\xbfid,label,agency_tag\r\nbus-1,Bus 1,north\r\nbus-2,\"Bus\r\n2\",\r\nbus-3,Bus 3,south\r\n")
	require.Equal(t, http.StatusOK, w.Code, pageText(w))
	require.Len(t, imp.vehiclePlans, 1)
	assert.Equal(t, []vehicleImportRow{
		{Line: 2, ID: "bus-1", Label: "Bus 1", AgencyTag: "north"},
		{Line: 3, ID: "bus-2", Label: "Bus\n2"},
		{Line: 5, ID: "bus-3", Label: "Bus 3", AgencyTag: "south"},
	}, imp.vehiclePlans[0])
}

func TestImportUpload_HeaderAndCells(t *testing.T) {
	imp := &fakeImporter{}
	ui := newImportTestUI(t, imp)
	w := uploadImport(t, ui, "vehicles", " Agency_Tag ,ID,label\n  north , bus-1 ,  Bus 1  \n")
	require.Equal(t, http.StatusOK, w.Code, pageText(w))
	require.Len(t, imp.vehiclePlans, 1)
	assert.Equal(t, []vehicleImportRow{{Line: 2, ID: "bus-1", Label: "Bus 1", AgencyTag: "north"}}, imp.vehiclePlans[0],
		"columns in any order and case, cells trimmed")
}

func TestImportUpload_FileErrors(t *testing.T) {
	cases := []struct {
		name, kind, file, want string
	}{
		{"empty", "vehicles", "", "the file is empty"},
		{"header only", "vehicles", vehiclesHeader, "the file has no rows under its header"},
		{"no header row", "vehicles", "bus-1,Bus 1,north\n", `the header row (the first line) is wrong: unknown column "bus-1"; unknown column "bus 1"; unknown column "north"; missing column "id"; missing column "label"; missing column "agency_tag"`},
		{"unknown column", "vehicles", "id,label,agency_tag,depot\nbus-1,Bus 1,,x\n", `unknown column "depot"`},
		{"missing column", "vehicles", "id,label\nbus-1,Bus 1\n", `missing column "agency_tag"`},
		{"column twice", "vehicles", "id,label,id,agency_tag\nbus-1,Bus 1,bus-1,\n", `column "id" appears twice`},
		{"column without a name", "vehicles", "id,label,,agency_tag\nbus-1,Bus 1,,\n", "column 3 has no name"},
		{"not UTF-8", "vehicles", vehiclesHeader + "bus-1,Bus \xe9,\n", "the file is not UTF-8 text: save it from your spreadsheet as CSV UTF-8"},
		{"bad quoting in the header", "vehicles", "id,la\"bel,agency_tag\n", `parse error on line 1, column 6: bare " in non-quoted-field`},
		{"bad quoting", "vehicles", vehiclesHeader + "bus-1,Bus \"1,\n", `parse error on line 2, column 11: bare " in non-quoted-field`},
		{"a role column", "drivers", "email,name,vehicle_ids,role\nana@example.org,Ana,,admin\n", `unknown column "role"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			imp := &fakeImporter{}
			ui := newImportTestUI(t, imp)
			w := uploadImport(t, ui, tc.kind, tc.file)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code)
			assert.Contains(t, pageText(w), tc.want)
			assert.Empty(t, imp.vehiclePlans)
			assert.Empty(t, imp.driverPlans)
		})
	}
}

// A drivers file can't carry passwords: the column is refused, and its value
// never comes back in the page.
func TestImportUpload_PasswordColumnRefused(t *testing.T) {
	imp := &fakeImporter{}
	ui := newImportTestUI(t, imp)
	w := uploadImport(t, ui, "drivers", "email,name,vehicle_ids,password\nana@example.org,Ana,,Hunter2-secret\n")
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, pageText(w), `unknown column "password"`)
	assert.NotContains(t, w.Body.String(), "Hunter2-secret")
	assert.Empty(t, imp.driverPlans)
}

func TestImportUpload_NotAFile(t *testing.T) {
	ui := newImportTestUI(t, &fakeImporter{})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("other", "x"))
	require.NoError(t, mw.Close())
	w := sendImport(t, ui, http.MethodPost, "/admin/import/vehicles", mw.FormDataContentType(), &buf)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, pageText(w), "choose a CSV file to upload")

	w = sendImport(t, ui, http.MethodPost, "/admin/import/vehicles", "application/x-www-form-urlencoded", strings.NewReader("file=x"))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, pageText(w), "Invalid form submission.")
}

func TestImportUpload_PreviewStatuses(t *testing.T) {
	imp := &fakeImporter{planned: []importOutcome{
		{Status: importNew},
		{Status: importUpdate, Changes: []string{"label was Old", "agency tag was empty"}},
		{Status: importUnchanged, Note: "stays deactivated"},
	}}
	ui := newImportTestUI(t, imp)
	w := uploadImport(t, ui, "vehicles", vehiclesHeader+"bus-1,A,\nbus-2,B,north\nbus-3,C,\n")
	require.Equal(t, http.StatusOK, w.Code)
	body := pageText(w)
	assert.Contains(t, body, "1 new, 1 to update, 1 unchanged, 0 with errors")
	assert.Contains(t, body, "label was Old; agency tag was empty")
	assert.Contains(t, body, "stays deactivated")
	assert.Contains(t, body, "Confirm import")
	assert.Contains(t, body, `onsubmit="this.querySelector('button').disabled = true"`, "a second click must not replace the result")
}

func TestImportUpload_NothingToImport(t *testing.T) {
	imp := &fakeImporter{planned: []importOutcome{{Status: importUnchanged}}}
	ui := newImportTestUI(t, imp)
	w := uploadImport(t, ui, "vehicles", vehiclesHeader+"bus-1,A,\n")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, pageText(w), "Nothing to import: every row matches what is saved.")
	assert.NotContains(t, w.Body.String(), `name="sig"`)
}

func TestImportUpload_StoreError(t *testing.T) {
	ui := newImportTestUI(t, &fakeImporter{err: errors.New("db down")})
	w := uploadImport(t, ui, "vehicles", vehiclesHeader+"bus-1,A,\n")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestImportConfirm_ImportsWhatWasPreviewed(t *testing.T) {
	imp := &fakeImporter{}
	ui := newImportTestUI(t, imp)
	preview := uploadImport(t, ui, "vehicles", vehiclesHeader+"bus-1,Bus 1,north\nbus-2,Bus 2,\n")
	require.Equal(t, http.StatusOK, preview.Code)

	w := confirmImport(t, ui, "vehicles", confirmForm(t, preview.Body.String()))
	require.Equal(t, http.StatusOK, w.Code, pageText(w))
	require.Len(t, imp.vehicleImports, 1)
	assert.Equal(t, imp.vehiclePlans[0], imp.vehicleImports[0])
	body := pageText(w)
	assert.Contains(t, body, "2 new, 0 updated, 0 unchanged")
	assert.Contains(t, body, "bus-2")
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestImportConfirm_DriversResult(t *testing.T) {
	imp := &fakeImporter{planned: []importOutcome{
		{Status: importNew},
		{Status: importUpdate, Changes: []string{"adds 102"}},
		{Status: importUnchanged},
	}}
	ui := newImportTestUI(t, imp)
	file := driversHeader + "ana@example.org,Ana,101\nbo@example.org,Bo,102\ncy@example.org,Cy,\n"
	w := confirmImport(t, ui, "drivers", signedForm("drivers", file, time.Now()))
	require.Equal(t, http.StatusOK, w.Code, pageText(w))
	body := pageText(w)

	m := regexp.MustCompile(`select-all">([^<]*)</span>`).FindAllStringSubmatch(body, -1)
	require.Len(t, m, 1, "one password, for the one new driver")
	assert.Regexp(t, driverPasswordPattern, m[0][1])
	assert.Contains(t, body, `href="/admin/driver-setup?user=100"`)
	assert.Contains(t, body, `href="/admin/driver-setup?user=101"`)
	assert.NotContains(t, body, "cy@example.org", "an unchanged driver isn't listed")
	assert.Contains(t, body, "1 new, 1 updated, 1 unchanged")
	assert.Contains(t, body, "These passwords are shown only on this page.")
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestImportConfirm_RefusesAChangedOrStalePreview(t *testing.T) {
	file := vehiclesHeader + "bus-1,Bus 1,\n"
	other := vehiclesHeader + "bus-9,Bus 9,\n"
	good := signedForm("vehicles", file, time.Now())
	otherKey := signImport([]byte("another-secret-of-at-least-32-bytes!"), "vehicles", time.Now(), []byte(file))
	cases := map[string]url.Values{
		"other data":       {"data": signedForm("vehicles", other, time.Now())["data"], "issued": good["issued"], "sig": good["sig"]},
		"other time":       {"data": good["data"], "issued": {strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)}, "sig": good["sig"]},
		"other kind":       signedForm("drivers", file, time.Now()),
		"expired":          signedForm("vehicles", file, time.Now().Add(-importPreviewLifetime-time.Minute)),
		"no signature":     {"data": good["data"], "issued": good["issued"]},
		"nothing":          {},
		"wrong secret key": {"data": {otherKey.Data}, "issued": {otherKey.Issued}, "sig": {otherKey.Sig}},
	}
	for name, form := range cases {
		t.Run(name, func(t *testing.T) {
			imp := &fakeImporter{}
			ui := newImportTestUI(t, imp)
			w := confirmImport(t, ui, "vehicles", form)
			require.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, pageText(w), "this preview has expired or was changed: upload the file again")
			assert.Empty(t, imp.vehicleImports)
		})
	}
}

func TestOpenImport(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	data := []byte(vehiclesHeader + "bus-1,Bus 1,\n")
	tok := signImport(testSecret, "vehicles", now, data)
	form := func(issued, data, sig string) url.Values {
		return url.Values{"issued": {issued}, "data": {data}, "sig": {sig}}
	}
	cases := []struct {
		name string
		form url.Values
		at   time.Time
		ok   bool
	}{
		{"fresh", form(tok.Issued, tok.Data, tok.Sig), now, true},
		{"at the lifetime", form(tok.Issued, tok.Data, tok.Sig), now.Add(importPreviewLifetime), true},
		{"past the lifetime", form(tok.Issued, tok.Data, tok.Sig), now.Add(importPreviewLifetime + time.Second), false},
		{"issued not a number", form("soon", tok.Data, tok.Sig), now, false},
		{"data not base64", form(tok.Issued, "%%%", tok.Sig), now, false},
		{"signature not base64", form(tok.Issued, tok.Data, "%%%"), now, false},
		{"signature of other data", form(tok.Issued, tok.Data, signImport(testSecret, "vehicles", now, []byte("x")).Sig), now, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, preview, err := openImport(testSecret, "vehicles", tc.form, tc.at)
			if tc.ok {
				require.NoError(t, err)
				assert.Equal(t, data, got)
				sig, err := base64.RawURLEncoding.DecodeString(tok.Sig)
				require.NoError(t, err)
				assert.Equal(t, importPreview{Signature: sig, Expires: now.Add(importPreviewLifetime)}, preview)
				return
			}
			assert.ErrorIs(t, err, errImportToken)
			assert.Nil(t, got)
			assert.Zero(t, preview)
		})
	}
}

// A confirm checks the file again rather than trusting its preview: a signed
// file with a bad row is refused and nothing reaches the store.
func TestImportConfirm_ChecksTheFileAgain(t *testing.T) {
	imp := &fakeImporter{}
	ui := newImportTestUI(t, imp)
	w := confirmImport(t, ui, "vehicles", signedForm("vehicles", vehiclesHeader+"bus-1,Bus 1,\nbus 2,Bus 2,\n", time.Now()))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	body := pageText(w)
	assert.Contains(t, body, "vehicle id must contain only alphanumeric characters, dots, hyphens, and underscores")
	assert.Contains(t, body, "Nothing was imported.")
	assert.Empty(t, imp.vehicleImports)

	w = confirmImport(t, ui, "vehicles", signedForm("vehicles", "id,label\nbus-1,Bus 1\n", time.Now()))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, pageText(w), `missing column "agency_tag"`)
	assert.Empty(t, imp.vehicleImports)
}

// A row the store refuses at the confirm (the database changed since the
// preview) brings the preview back with its reason.
func TestImportConfirm_StoreRefusesARow(t *testing.T) {
	imp := &fakeImporter{imported: []importOutcome{
		{Status: importNew},
		{Status: importError, Reason: "vehicle 102 is inactive"},
	}}
	ui := newImportTestUI(t, imp)
	w := confirmImport(t, ui, "drivers", signedForm("drivers", driversHeader+"ana@example.org,Ana,101\nbo@example.org,Bo,102\n", time.Now()))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	body := pageText(w)
	assert.Contains(t, body, "vehicle 102 is inactive")
	assert.Contains(t, body, "Nothing was imported.")
	assert.NotContains(t, body, `name="sig"`)
}

func TestImportConfirm_StoreError(t *testing.T) {
	ui := newImportTestUI(t, &fakeImporter{err: errors.New("db down")})
	w := confirmImport(t, ui, "vehicles", signedForm("vehicles", vehiclesHeader+"bus-1,A,\n", time.Now()))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestImportConfirm_TooLarge(t *testing.T) {
	imp := &fakeImporter{}
	ui := newImportTestUI(t, imp)
	w := confirmImport(t, ui, "vehicles", url.Values{"data": {strings.Repeat("A", 2*maxImportBytes)}})
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Contains(t, pageText(w), "the preview is too large to import: upload the file again")
	assert.Empty(t, imp.vehicleImports)

	w = sendImport(t, ui, http.MethodPost, "/admin/import/vehicles/confirm", "application/x-www-form-urlencoded", strings.NewReader("%zz"))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// The confirm's page is the only copy of the new passwords, so it gets more
// time than WRITE_TIMEOUT to be written while they hash.
func TestImportConfirm_OutlastsWriteTimeout(t *testing.T) {
	imp := &fakeImporter{delay: 600 * time.Millisecond}
	ui := newImportTestUI(t, imp)
	mux := http.NewServeMux()
	registerAdminUI(mux, ui)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.WriteTimeout = 200 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	form := signedForm("vehicles", vehiclesHeader+"bus-1,Bus 1,\n", time.Now())
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/import/vehicles/confirm", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieFor(t, "admin"))
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "1 new, 0 updated, 0 unchanged")
}

var driverPasswordPattern = regexp.MustCompile(`^[abcdefghjkmnpqrstuvwxyz23456789]{4}-[abcdefghjkmnpqrstuvwxyz23456789]{4}-[abcdefghjkmnpqrstuvwxyz23456789]{4}$`)

func TestGenerateDriverPassword(t *testing.T) {
	seen := make(map[string]struct{})
	counts := make(map[rune]int)
	for range 2000 {
		pw := generateDriverPassword()
		require.Regexp(t, driverPasswordPattern, pw)
		require.NoError(t, validatePassword(pw), "a made-up password must pass the policy every other password does")
		seen[pw] = struct{}{}
		for _, c := range strings.ReplaceAll(pw, "-", "") {
			counts[c]++
		}
	}
	assert.Len(t, seen, 2000)
	for _, c := range driverPasswordAlphabet {
		assert.Greater(t, counts[c], 0, "every character is drawn: %q", c)
	}
}

func TestHashPasswords(t *testing.T) {
	hashes, err := hashPasswords(context.Background(), map[int]string{0: "abcd-efgh-jkmn", 3: "pqrs-tuvw-xyz2"})
	require.NoError(t, err)
	require.Len(t, hashes, 2)
	require.NoError(t, bcrypt.CompareHashAndPassword(hashes[3], []byte("pqrs-tuvw-xyz2")))
	cost, err := bcrypt.Cost(hashes[0])
	require.NoError(t, err)
	assert.Equal(t, bcryptCost, cost)

	_, err = hashPasswords(context.Background(), map[int]string{0: strings.Repeat("x", 73)})
	assert.ErrorIs(t, err, bcrypt.ErrPasswordTooLong)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = hashPasswords(ctx, map[int]string{0: "abcd-efgh-jkmn"})
	assert.ErrorIs(t, err, context.Canceled)
}

// The confirm hands the store the preview's own signature and the end of its
// lifetime, which the store records so the preview is imported only once.
func TestImportConfirm_PassesThePreview(t *testing.T) {
	imp := &fakeImporter{}
	ui := newImportTestUI(t, imp)
	issued := time.Now().Add(-10 * time.Minute)
	form := signedForm("drivers", driversHeader+"ana@example.org,Ana,\n", issued)
	w := confirmImport(t, ui, "drivers", form)
	require.Equal(t, http.StatusOK, w.Code, pageText(w))

	sig, err := base64.RawURLEncoding.DecodeString(form.Get("sig"))
	require.NoError(t, err)
	require.Len(t, imp.previews, 1)
	assert.Equal(t, sig, imp.previews[0].Signature)
	assert.Equal(t, time.Unix(issued.Unix(), 0).Add(importPreviewLifetime), imp.previews[0].Expires)
}

func TestImportConfirm_AlreadyImported(t *testing.T) {
	ui := newImportTestUI(t, &fakeImporter{err: ErrPreviewImported})
	w := confirmImport(t, ui, "vehicles", signedForm("vehicles", vehiclesHeader+"bus-1,Bus 1,\n", time.Now()))
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, pageText(w), "this preview was already imported: to import the file again, upload it again")
	assert.NotContains(t, w.Body.String(), "Imported</h3>")
}
