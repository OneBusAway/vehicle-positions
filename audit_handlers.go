package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// auditEntryResponse is one row of the audit log in GET /api/v1/admin/audit.
type auditEntryResponse struct {
	ID int64     `json:"id"`
	At time.Time `json:"at"`
	// ActorID is null, and ActorEmail empty, for a change the server made
	// itself, which has no ActorIP either, and for a failed sign-in.
	ActorID    *int64 `json:"actor_id"`
	ActorEmail string `json:"actor_email"`
	// ActorIP is left out of a viewer's response, as the Audit log page leaves
	// the address out of a viewer's page.
	ActorIP    *string `json:"actor_ip,omitempty"`
	ObjectType string  `json:"object_type"`
	ObjectID   string  `json:"object_id"`
	Action     string  `json:"action"`
	// Before and After are null where the row has none: before a create, after
	// a delete, and for a new password.
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

// auditListResponse is one page of GET /api/v1/admin/audit.
type auditListResponse struct {
	Count   int                  `json:"count"`
	HasMore bool                 `json:"has_more"`
	Entries []auditEntryResponse `json:"entries"`
}

// handleListAuditEntries returns one page of the audit log, newest first,
// bounded by the limit/offset query params and narrowed by the Audit log
// page's filters, which parseAuditFilter reads for both. It fetches limit+1
// rows from the store to detect has_more without a separate count query, as
// handleListUsers does.
func handleListAuditEntries(store AuditLogLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset, ok := parseListPageParams(w, r)
		if !ok {
			return
		}

		filter, problem := parseAuditFilter(r.URL.Query())
		if problem != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
			return
		}
		// Fetch one extra row to detect whether results were truncated at limit.
		filter.Limit, filter.Offset = limit+1, offset

		entries, err := store.ListAuditEntries(r.Context(), filter)
		if err != nil {
			slog.Error("failed to list audit entries", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		hasMore := len(entries) > limit
		if hasMore {
			entries = entries[:limit]
		}

		viewer := isViewer(r)
		resp := make([]auditEntryResponse, 0, len(entries))
		for _, e := range entries {
			entry := auditEntryResponse{
				ID:         e.ID,
				At:         e.At.UTC(),
				ActorID:    e.ActorID,
				ActorEmail: e.ActorEmail,
				ObjectType: e.ObjectType,
				ObjectID:   e.ObjectID,
				Action:     e.Action,
				Before:     e.Before,
				After:      e.After,
			}
			if !viewer {
				ip := e.ActorIP
				entry.ActorIP = &ip
			}
			resp = append(resp, entry)
		}

		writeJSON(w, http.StatusOK, auditListResponse{
			Count:   len(resp),
			HasMore: hasMore,
			Entries: resp,
		})
	}
}
