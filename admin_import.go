package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// maxImportBytes caps an uploaded file. 200 rows of the longest valid
	// vehicles in ASCII fill less than half of it.
	maxImportBytes = 256 << 10
	// maxImportRows caps a file's rows. Each new driver's password takes about
	// 0.1 s to hash on one core, so a file of new drivers takes up to 20 s on a
	// small server.
	maxImportRows = 200
	// importFormOverhead is room for the upload form's other bytes: the
	// multipart boundaries and part headers.
	importFormOverhead = 64 << 10
	// importPreviewLifetime is how long a preview can be confirmed.
	importPreviewLifetime = time.Hour
	// importWriteTimeout replaces WRITE_TIMEOUT for a confirm, whose page is
	// the only copy of the new drivers' passwords and must not be cut off
	// while they hash.
	importWriteTimeout = 2 * time.Minute
)

// importColumn is a column of an import file, with the help the upload page
// shows for it.
type importColumn struct {
	Name string
	Help string
}

// importKind is one of the two imports. preview checks a file and plans it
// without writing; run checks it again and imports it.
type importKind struct {
	Name  string
	Title string
	Page  string
	// ListURL and ListName are the list page an import belongs to, which the
	// pages link back to.
	ListURL  string
	ListName string
	// One and Many name what a row of the file is: "vehicle", "vehicles".
	One, Many string
	Columns   []importColumn
	Notes     []string
	preview   func(context.Context, CSVImporter, []importRecord) ([]importRowView, error)
	run       func(context.Context, CSVImporter, importPreview, []importRecord) ([]importRowView, error)
}

func (k importKind) columnNames() []string {
	names := make([]string, len(k.Columns))
	for i, c := range k.Columns {
		names[i] = c.Name
	}
	return names
}

var importKinds = map[string]importKind{
	"vehicles": {
		Name:     "vehicles",
		Title:    "Import vehicles",
		Page:     "vehicles",
		ListURL:  "/admin/vehicles",
		ListName: "Vehicles",
		One:      "vehicle",
		Many:     "vehicles",
		Columns: []importColumn{
			{"id", "Required. Letters, digits, dots, hyphens and underscores, up to 50 characters."},
			{"label", "The name shown in the admin pages, up to 255 characters."},
			{"agency_tag", "Optional grouping, up to 255 characters."},
		},
		Notes: []string{
			"A vehicle whose id is already saved takes the file's label and agency tag. An import never deactivates or reactivates a vehicle.",
		},
		preview: func(ctx context.Context, imp CSVImporter, recs []importRecord) ([]importRowView, error) {
			return previewRows(ctx, recs, vehicleRows, imp.PlanVehicleImport)
		},
		run: func(ctx context.Context, imp CSVImporter, preview importPreview, recs []importRecord) ([]importRowView, error) {
			return runRows(ctx, recs, vehicleRows, func(ctx context.Context, rows []vehicleImportRow) ([]importOutcome, error) {
				return imp.ImportVehicles(ctx, preview, rows)
			})
		},
	},
	"drivers": {
		Name:     "drivers",
		Title:    "Import drivers",
		Page:     "users",
		ListURL:  "/admin/users",
		ListName: "Drivers and users",
		One:      "driver",
		Many:     "drivers",
		Columns: []importColumn{
			{"email", "Required. Saved in lower case, and matched to an existing account whatever its case."},
			{"name", "Required."},
			{"vehicle_ids", "Optional. The vehicles to assign, separated by spaces, such as 101 102. Each must already be saved and active."},
		},
		Notes: []string{
			"Each new driver gets a password made up by the server. It is shown once, after the import, next to the driver's setup sheet.",
			"For a driver who already has an account, only the name changes and the listed vehicles are added. The password and the role never change, and an import never removes a vehicle or creates an admin.",
		},
		preview: func(ctx context.Context, imp CSVImporter, recs []importRecord) ([]importRowView, error) {
			return previewRows(ctx, recs, driverRows, imp.PlanDriverImport)
		},
		run: func(ctx context.Context, imp CSVImporter, preview importPreview, recs []importRecord) ([]importRowView, error) {
			return runRows(ctx, recs, driverRows, func(ctx context.Context, rows []driverImportRow) ([]importOutcome, error) {
				return imp.ImportDrivers(ctx, preview, rows, generateDriverPassword)
			})
		},
	},
}

// importRecord is one line of a file under its header, with its cells
// trimmed and keyed by column. Problem is set when the line doesn't have one
// cell per column.
type importRecord struct {
	Line    int
	Cells   map[string]string
	Problem string
}

// readImportFile reads an uploaded file: UTF-8, with or without a byte order
// mark, CRLF or LF line ends, and a header row naming exactly columns, in any
// order. Lines whose cells are all blank are skipped, as spreadsheets leave
// them at the end. The error is about the whole file.
func readImportFile(data []byte, columns []string) ([]importRecord, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		return nil, errors.New("the file is not UTF-8 text: save it from your spreadsheet as CSV UTF-8")
	}
	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, errors.New("the file is empty")
	}
	if err != nil {
		return nil, err
	}
	index, err := importHeader(header, columns)
	if err != nil {
		return nil, err
	}

	var recs []importRecord
	for {
		cells, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(cells, func(c string) bool { return strings.TrimSpace(c) != "" }) {
			continue
		}
		if len(recs) == maxImportRows {
			return nil, fmt.Errorf("the file has more than %d rows: split it into files of %d rows or fewer", maxImportRows, maxImportRows)
		}
		line, _ := cr.FieldPos(0)
		rec := importRecord{Line: line, Cells: make(map[string]string, len(columns))}
		if len(cells) != len(header) {
			rec.Problem = fmt.Sprintf("this line has %d cells and the header has %d", len(cells), len(header))
		}
		for name, i := range index {
			if i < len(cells) {
				rec.Cells[name] = strings.TrimSpace(cells[i])
			}
		}
		recs = append(recs, rec)
	}
	if len(recs) == 0 {
		return nil, errors.New("the file has no rows under its header")
	}
	return recs, nil
}

// importHeader maps each of columns to its place in header, whose names are
// matched without regard to case or surrounding spaces.
func importHeader(header, columns []string) (map[string]int, error) {
	index := make(map[string]int, len(columns))
	var problems []string
	for i, raw := range header {
		name := strings.ToLower(strings.TrimSpace(raw))
		_, seen := index[name]
		switch {
		case name == "":
			problems = append(problems, fmt.Sprintf("column %d has no name", i+1))
		case !slices.Contains(columns, name):
			problems = append(problems, fmt.Sprintf("unknown column %q", name))
		case seen:
			problems = append(problems, fmt.Sprintf("column %q appears twice", name))
		default:
			index[name] = i
		}
	}
	for _, name := range columns {
		if _, ok := index[name]; !ok {
			problems = append(problems, fmt.Sprintf("missing column %q", name))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("the header row (the first line) is wrong: %s", strings.Join(problems, "; "))
	}
	return index, nil
}

// importRowView is a row of the preview or result table.
type importRowView struct {
	Line     int
	Cells    []string
	Status   importStatus
	Reason   string
	Changes  string
	Note     string
	UserID   int64
	Password string
}

func (v *importRowView) fail(reason string) {
	v.Status, v.Reason = importError, reason
}

func (v *importRowView) take(o importOutcome) {
	v.Status, v.Reason, v.Note = o.Status, o.Reason, o.Note
	v.Changes = strings.Join(o.Changes, "; ")
	v.UserID, v.Password = o.UserID, o.Password
}

// vehicleRows checks each line with the vehicles API's own validation, which
// the vehicle form's id check is part of.
func vehicleRows(recs []importRecord) ([]vehicleImportRow, []importRowView) {
	rows := make([]vehicleImportRow, len(recs))
	views := make([]importRowView, len(recs))
	firstLine := make(map[string]int, len(recs))
	for i, rec := range recs {
		row := vehicleImportRow{Line: rec.Line, ID: rec.Cells["id"], Label: rec.Cells["label"], AgencyTag: rec.Cells["agency_tag"]}
		rows[i] = row
		views[i] = importRowView{Line: rec.Line, Cells: []string{row.ID, row.Label, row.AgencyTag}}
		req := upsertVehicleRequest{ID: row.ID, Label: row.Label, AgencyTag: row.AgencyTag}
		if rec.Problem != "" {
			views[i].fail(rec.Problem)
		} else if err := req.validate(); err != nil {
			views[i].fail(err.Error())
		} else if line, ok := firstLine[row.ID]; ok {
			views[i].fail(fmt.Sprintf("vehicle id is already on line %d", line))
		} else {
			firstLine[row.ID] = rec.Line
		}
	}
	return rows, views
}

// driverRows checks each line as the users API checks a new user, with the
// same messages, after lower-casing the email. vehicle_ids is split on spaces,
// which no vehicle id can hold, and each id is checked as the vehicle form
// checks one.
func driverRows(recs []importRecord) ([]driverImportRow, []importRowView) {
	rows := make([]driverImportRow, len(recs))
	views := make([]importRowView, len(recs))
	firstLine := make(map[string]int, len(recs))
	for i, rec := range recs {
		var ids []string
		for _, id := range strings.Fields(rec.Cells["vehicle_ids"]) {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		row := driverImportRow{Line: rec.Line, Email: strings.ToLower(rec.Cells["email"]), Name: rec.Cells["name"], VehicleIDs: ids}
		rows[i] = row
		views[i] = importRowView{Line: rec.Line, Cells: []string{row.Email, row.Name, strings.Join(ids, " ")}}
		if rec.Problem != "" {
			views[i].fail(rec.Problem)
		} else if row.Name == "" {
			views[i].fail("name is required")
		} else if row.Email == "" {
			views[i].fail("email is required")
		} else if !validEmail(row.Email) {
			views[i].fail("email is not valid")
		} else if err := validateVehicleIDs(ids); err != nil {
			views[i].fail(err.Error())
		} else if line, ok := firstLine[row.Email]; ok {
			views[i].fail(fmt.Sprintf("email is already on line %d", line))
		} else {
			firstLine[row.Email] = rec.Line
		}
	}
	return rows, views
}

func validateVehicleIDs(ids []string) error {
	for _, id := range ids {
		if err := validateVehicleID(id); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
	}
	return nil
}

// previewRows plans the rows that passed their checks, so the preview shows
// what the database would say about each.
func previewRows[T any](ctx context.Context, recs []importRecord, check func([]importRecord) ([]T, []importRowView), plan func(context.Context, []T) ([]importOutcome, error)) ([]importRowView, error) {
	rows, views := check(recs)
	var valid []T
	var at []int
	for i := range views {
		if views[i].Status != importError {
			valid = append(valid, rows[i])
			at = append(at, i)
		}
	}
	outcomes, err := plan(ctx, valid)
	if err != nil {
		return nil, err
	}
	for j, o := range outcomes {
		views[at[j]].take(o)
	}
	return views, nil
}

// runRows checks every row again and imports them only when all pass.
func runRows[T any](ctx context.Context, recs []importRecord, check func([]importRecord) ([]T, []importRowView), run func(context.Context, []T) ([]importOutcome, error)) ([]importRowView, error) {
	rows, views := check(recs)
	if slices.ContainsFunc(views, func(v importRowView) bool { return v.Status == importError }) {
		return views, nil
	}
	outcomes, err := run(ctx, rows)
	if err != nil {
		return nil, err
	}
	for i, o := range outcomes {
		views[i].take(o)
	}
	return views, nil
}

// importCounts counts a table's rows by status.
type importCounts struct {
	New, Update, Unchanged, Error int
}

// ErrorRows is the check page's count of rows the file has to change: "1
// row", "2 rows".
func (c importCounts) ErrorRows() string {
	return countOf(c.Error, "row", "rows")
}

// The step line above each of an import's three pages.
var (
	importStepUpload = stepLine{N: 1, Of: 3, Name: "Upload", Hint: "upload the file"}
	importStepCheck  = stepLine{N: 2, Of: 3, Name: "Check", Hint: "check what it changes"}
	importStepDone   = stepLine{N: 3, Of: 3, Name: "Done", Hint: "done"}
)

// importDoneHeadline is the result page's headline: how many rows the import
// made new.
func importDoneHeadline(kind importKind, c importCounts) string {
	if c.New == 0 {
		return "Import finished"
	}
	return countOf(c.New, kind.One, kind.Many) + " imported"
}

func countImport(views []importRowView) importCounts {
	var c importCounts
	for _, v := range views {
		switch v.Status {
		case importNew:
			c.New++
		case importUpdate:
			c.Update++
		case importUnchanged:
			c.Unchanged++
		case importError:
			c.Error++
		}
	}
	return c
}

// importToken carries a previewed file to the confirm step in the page
// itself, signed, so the confirm imports exactly the file that was
// previewed without the admin choosing it again or the server keeping it.
type importToken struct {
	Data   string
	Issued string
	Sig    string
}

var errImportToken = errors.New("the preview has expired or was changed")

// importMAC signs a previewed file for its kind and time, with a key of its
// own derived from the JWT secret.
func importMAC(secret []byte, kind string, issued int64, data []byte) []byte {
	key := hmac.New(sha256.New, secret)
	key.Write([]byte("admin csv import preview"))
	m := hmac.New(sha256.New, key.Sum(nil))
	fmt.Fprintf(m, "%s\n%d\n", kind, issued)
	m.Write(data)
	return m.Sum(nil)
}

func signImport(secret []byte, kind string, now time.Time, data []byte) importToken {
	issued := now.Unix()
	return importToken{
		Data:   base64.RawURLEncoding.EncodeToString(data),
		Issued: strconv.FormatInt(issued, 10),
		Sig:    base64.RawURLEncoding.EncodeToString(importMAC(secret, kind, issued, data)),
	}
}

// openImport returns the file a confirm form carries, and the preview it
// comes from, if the signature holds and the preview is less than
// importPreviewLifetime old.
func openImport(secret []byte, kind string, form url.Values, now time.Time) ([]byte, importPreview, error) {
	issued, err := strconv.ParseInt(form.Get("issued"), 10, 64)
	if err != nil {
		return nil, importPreview{}, errImportToken
	}
	data, err := base64.RawURLEncoding.DecodeString(form.Get("data"))
	if err != nil {
		return nil, importPreview{}, errImportToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(form.Get("sig"))
	if err != nil || !hmac.Equal(sig, importMAC(secret, kind, issued, data)) {
		return nil, importPreview{}, errImportToken
	}
	expires := time.Unix(issued, 0).Add(importPreviewLifetime)
	if now.After(expires) {
		return nil, importPreview{}, errImportToken
	}
	return data, importPreview{Signature: sig, Expires: expires}, nil
}

// driverPasswordAlphabet leaves out 0, 1, i, l and o, which are easy to
// misread when the password is copied by hand.
const driverPasswordAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// generateDriverPassword makes a new driver's password: twelve random
// characters in groups of four, such as k7mq-x4tr-9whp. That is about 59 bits,
// and quick to type on a phone.
func generateDriverPassword() string {
	const n = len(driverPasswordAlphabet)
	// A byte at or past the last whole multiple of n is drawn again, so each
	// character is equally likely.
	const limit = 256 - 256%n
	var b strings.Builder
	var c [1]byte
	for chars := 0; chars < 12; {
		rand.Read(c[:]) // never fails since Go 1.24
		if int(c[0]) >= limit {
			continue
		}
		if chars > 0 && chars%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(driverPasswordAlphabet[int(c[0])%n])
		chars++
	}
	return b.String()
}

func (ui *adminUI) importKindFor(w http.ResponseWriter, r *http.Request) (importKind, bool) {
	kind, ok := importKinds[r.PathValue("kind")]
	if !ok {
		http.NotFound(w, r)
	}
	return kind, ok
}

func (ui *adminUI) renderImport(w http.ResponseWriter, r *http.Request, status int, kind importKind, data map[string]interface{}) {
	data["Title"] = kind.Title
	data["Page"] = kind.Page
	data["Kind"] = kind
	data["MaxRows"] = maxImportRows
	data["MaxKB"] = maxImportBytes >> 10
	ui.renderAdmin(w, r, status, "import.html", data)
}

func (ui *adminUI) renderImportForm(w http.ResponseWriter, r *http.Request, status int, kind importKind, errMsg string) {
	ui.renderImport(w, r, status, kind, map[string]interface{}{"Step": importStepUpload, "Error": errMsg})
}

// importPage shows the upload form.
func (ui *adminUI) importPage(w http.ResponseWriter, r *http.Request) {
	if kind, ok := ui.importKindFor(w, r); ok {
		ui.renderImportForm(w, r, http.StatusOK, kind, "")
	}
}

// importTemplate downloads an empty file with the kind's header row.
func (ui *adminUI) importTemplate(w http.ResponseWriter, r *http.Request) {
	kind, ok := ui.importKindFor(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+kind.Name+`-template.csv"`)
	if _, err := io.WriteString(w, strings.Join(kind.columnNames(), ",")+"\r\n"); err != nil {
		slog.Error("import template: write", "kind", kind.Name, "error", err)
	}
}

func isTooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(err, &tooLarge)
}

// importUpload reads an uploaded file and shows its preview: every line with
// what importing it would do, or why it can't be imported.
func (ui *adminUI) importUpload(w http.ResponseWriter, r *http.Request) {
	kind, ok := ui.importKindFor(w, r)
	if !ok {
		return
	}
	tooLarge := fmt.Sprintf("the file is larger than %d KB: split it into smaller files", maxImportBytes>>10)
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+importFormOverhead)
	if err := r.ParseMultipartForm(maxImportBytes + importFormOverhead); err != nil {
		if isTooLarge(err) {
			ui.renderImportForm(w, r, http.StatusRequestEntityTooLarge, kind, tooLarge)
			return
		}
		ui.renderImportForm(w, r, http.StatusBadRequest, kind, "Invalid form submission.")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		ui.renderImportForm(w, r, http.StatusUnprocessableEntity, kind, "choose a CSV file to upload")
		return
	}
	defer file.Close()
	if header.Size > maxImportBytes {
		ui.renderImportForm(w, r, http.StatusRequestEntityTooLarge, kind, tooLarge)
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		ui.renderImportForm(w, r, http.StatusBadRequest, kind, "Invalid form submission.")
		return
	}

	recs, err := readImportFile(data, kind.columnNames())
	if err != nil {
		ui.renderImportForm(w, r, http.StatusUnprocessableEntity, kind, err.Error())
		return
	}
	views, err := kind.preview(r.Context(), ui.importer, recs)
	if err != nil {
		slog.Error("import preview: plan", "kind", kind.Name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	counts := countImport(views)
	page := map[string]interface{}{"Step": importStepCheck, "Rows": views, "Counts": counts}
	status := http.StatusOK
	if counts.Error > 0 {
		status = http.StatusUnprocessableEntity
	} else if counts.New+counts.Update > 0 {
		page["Token"] = signImport(ui.jwtSecret, kind.Name, time.Now(), data)
	}
	ui.renderImport(w, r, status, kind, page)
}

// importConfirm imports the file a preview carries. Everything is checked
// again, since the database may have changed since the preview, and either
// every row is written or, when any row fails, none is and the preview comes
// back with the reasons.
func (ui *adminUI) importConfirm(w http.ResponseWriter, r *http.Request) {
	kind, ok := ui.importKindFor(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2*maxImportBytes)
	if err := r.ParseForm(); err != nil {
		if isTooLarge(err) {
			ui.renderImportForm(w, r, http.StatusRequestEntityTooLarge, kind, "the preview is too large to import: upload the file again")
			return
		}
		ui.renderImportForm(w, r, http.StatusBadRequest, kind, "Invalid form submission.")
		return
	}
	data, preview, err := openImport(ui.jwtSecret, kind.Name, r.PostForm, time.Now())
	if err != nil {
		ui.renderImportForm(w, r, http.StatusBadRequest, kind, "this preview has expired or was changed: upload the file again")
		return
	}
	// A writer that can't move its deadline keeps WRITE_TIMEOUT.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(importWriteTimeout))

	recs, err := readImportFile(data, kind.columnNames())
	if err != nil {
		ui.renderImportForm(w, r, http.StatusUnprocessableEntity, kind, err.Error())
		return
	}
	views, err := kind.run(r.Context(), ui.importer, preview, recs)
	if errors.Is(err, ErrPreviewImported) {
		ui.renderImportForm(w, r, http.StatusConflict, kind, "this preview was already imported: to import the file again, upload it again")
		return
	}
	if err != nil {
		slog.Error("import: write", "kind", kind.Name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	counts := countImport(views)
	if counts.Error > 0 {
		ui.renderImport(w, r, http.StatusUnprocessableEntity, kind, map[string]interface{}{
			"Step": importStepCheck, "Rows": views, "Counts": counts, "NotImported": true,
		})
		return
	}
	slog.Info("admin import", "kind", kind.Name, "new", counts.New, "updated", counts.Update, "unchanged", counts.Unchanged)

	var done []importRowView
	for _, v := range views {
		if v.Status == importNew || v.Status == importUpdate {
			done = append(done, v)
		}
	}
	page := map[string]interface{}{
		"Step": importStepDone, "Result": true, "Done": done, "Counts": counts,
		"Headline": importDoneHeadline(kind, counts),
	}
	if kind.Name == "drivers" && len(done) > 0 {
		page["Sheets"] = importSheets(r, ui.cfg.trustProxy, done)
	}
	// The page holds the new drivers' passwords, so no cache keeps a copy.
	w.Header().Set("Cache-Control", "no-store")
	ui.renderImport(w, r, http.StatusOK, kind, page)
}
