package main

import (
	"cmp"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// auditObjectTypes are the kinds of object the Audit log page filters by, in
// the order its chips show them, with each chip's label and the noun the
// headline names one of them by.
var auditObjectTypes = []struct{ kind, label, noun string }{
	{auditVehicle, "Vehicles", "vehicle"},
	{auditUser, "Accounts", "account"},
	{auditTrip, "Trips", "trip"},
	{auditAPIKey, "API keys", "API key"},
}

// auditActions are what each change is called on the page, by kind of
// object and action. A row whose pair is missing shows the action as stored.
var auditActions = map[string]map[string]string{
	auditVehicle: {
		auditCreate:     "Added vehicle",
		auditUpdate:     "Edited vehicle",
		auditActivate:   "Activated vehicle",
		auditDeactivate: "Deactivated vehicle",
	},
	auditUser: {
		auditCreate:     "Created account",
		auditUpdate:     "Edited account",
		auditPassword:   "Set a new password for",
		auditActivate:   "Activated account",
		auditDeactivate: "Deactivated account",
		auditDelete:     "Deleted account",
		auditAssign:     "Assigned a vehicle to",
		auditUnassign:   "Removed a vehicle from",
	},
	auditTrip: {
		auditEnd: "Ended trip",
	},
	auditAPIKey: {
		auditCreate:     "Created API key",
		auditDeactivate: "Deactivated API key",
	},
}

// auditFieldLabels are the stored fields the page shows, in the order it
// shows them, with each one's name in words.
var auditFieldLabels = []struct{ field, label string }{
	{"name", "Name"},
	{"label", "Label"},
	{"email", "Email"},
	{"role", "Role"},
	{"agency_tag", "Agency tag"},
	{"vehicle_id", "Vehicle"},
	{"status", "Status"},
	{"active", "Status"},
}

// auditDetail is one field of a row: what it was and what it became. From
// is empty for a field set by a create or an assignment, To for one removed
// by a delete or an unassignment.
type auditDetail struct {
	Label string
	From  string
	To    string
}

// auditRow is one change on the Audit log page.
type auditRow struct {
	// When and Day are on the page's clock.
	When string
	Day  string
	// Who is the admin's email, or "Server" for a change the server made
	// itself; WhoURL lists every change they made, "" for the server.
	Who    string
	WhoURL string
	// IP is the address the change came from: "" for the server's own, and
	// on a viewer's page.
	IP string
	// What is the change in words, and Object the object it was made to,
	// with ObjectURL listing that object's history.
	What      string
	Object    string
	ObjectURL string
	Details   []auditDetail
	// name is the object's name from the row's fields, "" when they have
	// none.
	name string
}

// auditPageURL is an /admin/audit link for the filter f's kind of object,
// object and admin, at page.
func auditPageURL(f auditFilter, page int) string {
	v := url.Values{}
	if f.ObjectType != "" {
		v.Set("type", f.ObjectType)
	}
	if f.ObjectID != "" {
		v.Set("id", f.ObjectID)
	}
	if f.ActorID != nil {
		v.Set("actor", strconv.FormatInt(*f.ActorID, 10))
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if len(v) == 0 {
		return "/admin/audit"
	}
	return "/admin/audit?" + v.Encode()
}

// auditTypeNoun is kind in words, and false when there is no such kind.
func auditTypeNoun(kind string) (string, bool) {
	for _, t := range auditObjectTypes {
		if t.kind == kind {
			return t.noun, true
		}
	}
	return "", false
}

// parseAuditFilter reads the audit log's filter from a query, the Audit log
// page's and GET /api/v1/admin/audit's alike: ?type= one kind of object, with
// ?id= one object of it, and ?actor= one admin's user id. It returns a
// message for the 400 when the query names no such filter.
func parseAuditFilter(q url.Values) (auditFilter, string) {
	f := auditFilter{ObjectType: q.Get("type"), ObjectID: q.Get("id")}
	if _, ok := auditTypeNoun(f.ObjectType); f.ObjectType != "" && !ok {
		return auditFilter{}, "type must be vehicle, user, trip or api_key"
	}
	if f.ObjectID != "" && f.ObjectType == "" {
		return auditFilter{}, "id needs a type"
	}
	if raw := q.Get("actor"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			return auditFilter{}, "actor must be a user id"
		}
		f.ActorID = &id
	}
	return f, ""
}

// newAuditRow is e as the page shows it, on the clock loc, as of now.
func newAuditRow(e auditEntry, loc *time.Location, now time.Time) auditRow {
	before, after := auditFields(e.Before), auditFields(e.After)
	// A delete has only the fields from before it to name its object by.
	named := after
	if named == nil {
		named = before
	}
	row := auditRow{
		When:      e.At.In(loc).Format("15:04"),
		Day:       formatDay(e.At, loc, now),
		Who:       "Server",
		IP:        e.ActorIP,
		What:      cmp.Or(auditActions[e.ObjectType][e.Action], e.Action),
		Object:    e.ObjectID,
		ObjectURL: auditPageURL(auditFilter{ObjectType: e.ObjectType, ObjectID: e.ObjectID}, 1),
		Details:   auditDetails(before, after),
		// A row with no name of its own, such as a new password's, names an
		// account by what it is called now.
		name: cmp.Or(auditObjectName(e.ObjectType, named), e.AccountName),
	}
	switch {
	case row.name != "":
		row.Object = row.name
	case e.ObjectType == auditUser:
		// The words before it name the kind, but for an account they can
		// end in "for", "to" or "from".
		row.Object = "account " + e.ObjectID
	}
	if e.ActorID != nil {
		row.Who = cmp.Or(e.ActorEmail, "account "+strconv.FormatInt(*e.ActorID, 10))
		row.WhoURL = auditPageURL(auditFilter{ActorID: e.ActorID}, 1)
	}
	return row
}

// auditFields decodes a row's stored fields, nil for none.
func auditFields(raw json.RawMessage) map[string]any {
	if raw == nil {
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		slog.Warn("audit log: unreadable fields", "error", err)
		return nil
	}
	return fields
}

// auditObjectName is the name fields give an object of kind: a vehicle's
// label, or an account's or a key's name. It is "" when they give none.
func auditObjectName(kind string, fields map[string]any) string {
	var name string
	switch kind {
	case auditVehicle:
		name, _ = fields["label"].(string)
	case auditUser, auditAPIKey:
		name, _ = fields["name"].(string)
	}
	return name
}

// auditDetails lists a row's fields: every field a create or an assignment
// set, every field a delete or an unassignment removed, and the fields an
// edit changed.
func auditDetails(before, after map[string]any) []auditDetail {
	details := make([]auditDetail, 0)
	for _, f := range auditFieldLabels {
		from, hadBefore := auditValue(f.field, before)
		to, hasAfter := auditValue(f.field, after)
		switch {
		case !hadBefore && !hasAfter:
			continue
		case before != nil && after != nil && from == to:
			continue
		}
		details = append(details, auditDetail{Label: f.label, From: from, To: to})
	}
	return details
}

// auditValue is field's value in fields in words, and false when fields
// does not have it.
func auditValue(field string, fields map[string]any) (string, bool) {
	v, ok := fields[field]
	if !ok {
		return "", false
	}
	switch v := v.(type) {
	case bool:
		if v {
			return "Active", true
		}
		return "Deactivated", true
	case string:
		switch {
		case v == "":
			return "none", true
		case field == "role":
			return strings.ToUpper(v[:1]) + v[1:], true
		case field == "status" && v == "active":
			return "Running", true
		case field == "status" && v == "completed":
			return "Ended", true
		}
		return v, true
	}
	// The log stores only text and true or false.
	return "", false
}

// auditHeadline is the Audit log page's headline for the filter f, naming
// the object or admin as the newest row shown does, when there is one.
func auditHeadline(f auditFilter, rows []auditRow) string {
	switch {
	case f.ObjectID != "":
		if len(rows) > 0 && rows[0].name != "" {
			return "History of " + rows[0].name
		}
		noun, _ := auditTypeNoun(f.ObjectType)
		return "History of " + noun + " " + f.ObjectID
	case f.ActorID != nil:
		if len(rows) > 0 {
			return "Changes by " + rows[0].Who
		}
		return "Changes by account " + strconv.FormatInt(*f.ActorID, 10)
	}
	return "Who changed what"
}

// auditPage renders one page of the audit log, newest first, of every kind of
// object or of the one ?type= names, narrowed to one object by ?id= or to one
// admin's changes by ?actor=.
func (ui *adminUI) auditPage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter, problem := parseAuditFilter(query)
	if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return
	}
	page := adminPageNumber(query)

	filter.Limit, filter.Offset = adminPageSize+1, adminPageOffset(page)
	entries, err := ui.auditLog.ListAuditEntries(r.Context(), filter)
	if err != nil {
		slog.Error("audit: list audit entries", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	hasMore := len(entries) > adminPageSize
	if hasMore {
		entries = entries[:adminPageSize]
	}

	loc := pageLocation(ui.reports.index())
	now := time.Now()
	// A viewer sees who made each change but not the address they made it
	// from, which says where an admin was. It is left out of the page, not
	// just hidden.
	viewer := isViewer(r)
	rows := make([]auditRow, 0, len(entries))
	for _, e := range entries {
		row := newAuditRow(e, loc, now)
		if viewer {
			row.IP = ""
		}
		rows = append(rows, row)
	}

	// A chip shows every change to its kind of object, so it drops the
	// object and admin filters.
	chips := make([]statusLink, 0, len(auditObjectTypes)+1)
	chips = append(chips, statusLink{Label: "All", URL: auditPageURL(auditFilter{}, 1), Current: filter.ObjectType == "" && filter.ActorID == nil})
	for _, t := range auditObjectTypes {
		chips = append(chips, statusLink{
			Label:   t.label,
			URL:     auditPageURL(auditFilter{ObjectType: t.kind}, 1),
			Current: filter.ObjectType == t.kind && filter.ObjectID == "" && filter.ActorID == nil,
		})
	}

	ui.renderAdmin(w, r, http.StatusOK, "audit.html", map[string]interface{}{
		"Title":    "Audit log",
		"Page":     "audit",
		"Headline": auditHeadline(filter, rows),
		"Rows":     rows,
		"Chips":    chips,
		"Filtered": filter.ObjectType != "" || filter.ActorID != nil,
		"Timezone": loc.String(),
		"Shown":    newPagerCount(len(rows), "change", "changes"),
		"PageNum":  page,
		"HasMore":  hasMore,
		"PrevURL":  auditPageURL(filter, page-1),
		"NextURL":  auditPageURL(filter, page+1),
	})
}
