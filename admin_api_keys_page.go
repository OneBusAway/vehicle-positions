package main

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
)

// apiKeyRow is a single row in the Feed API keys table. There is no key
// column: only a key's hash is stored, so the page can never show one again.
type apiKeyRow struct {
	ID       int64
	Name     string
	Created  string
	LastUsed string
	Active   bool
}

// apiKeyForm is the state of the create form on the Feed API keys page: the
// submitted name and error to re-render after a refused submission, or the
// new key after an accepted one. NewKey is set only on the response to the
// POST that made the key.
type apiKeyForm struct {
	Name       string
	Error      string
	NewKey     string
	NewKeyName string
}

func apiKeysPageURL(page int) string {
	return "/admin/api-keys?page=" + strconv.Itoa(page)
}

// renderAPIKeys renders one page of keys with the create form above it.
// ListAPIKeys returns every key, so the paging is done here, over a list
// that is already in memory.
func (ui *adminUI) renderAPIKeys(w http.ResponseWriter, r *http.Request, status int, keys []APIKey, page int, form apiKeyForm) {
	offset := min(adminPageOffset(page), len(keys))
	keys = keys[offset:]
	hasMore := len(keys) > adminPageSize
	if hasMore {
		keys = keys[:adminPageSize]
	}

	rows := make([]apiKeyRow, 0, len(keys))
	for _, k := range keys {
		row := apiKeyRow{
			ID:       k.ID,
			Name:     k.Name,
			Created:  formatTripTimestamp(k.CreatedAt),
			LastUsed: "never",
			Active:   k.Active,
		}
		if k.LastUsedAt != nil {
			row.LastUsed = formatTripTimestamp(*k.LastUsedAt)
		}
		rows = append(rows, row)
	}

	ui.renderAdmin(w, r, status, "api_keys.html", map[string]interface{}{
		"Title":      "Feed API keys",
		"Page":       "api-keys",
		"Keys":       rows,
		"Name":       form.Name,
		"Error":      form.Error,
		"NewKey":     form.NewKey,
		"NewKeyName": form.NewKeyName,
		"PageNum":    page,
		"HasMore":    hasMore,
		"PrevURL":    apiKeysPageURL(page - 1),
		"NextURL":    apiKeysPageURL(page + 1),
	})
}

// apiKeysPage renders the feed API keys, newest first, with the form that
// makes a new one.
func (ui *adminUI) apiKeysPage(w http.ResponseWriter, r *http.Request) {
	keys, err := ui.apiKeys.ListAPIKeys(r.Context())
	if err != nil {
		slog.Error("api keys: list api keys", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	ui.renderAPIKeys(w, r, http.StatusOK, keys, adminPageNumber(r.URL.Query()), apiKeyForm{})
}

// apiKeyCreate makes a key from the form's name and answers with the list
// page carrying the raw key. There is no redirect: the key is not stored, so
// it could only cross one in a URL or a cookie, and this response is the one
// place it is ever shown.
//
// The name is checked by createAPIKeyRequest.validate and the key is made by
// issueAPIKey, the same two the JSON API uses, so the form and the API cannot
// disagree about either.
//
// The list is read before the key is made. Reading it afterwards would leave
// a failed read with a stored key and no page to show it on.
func (ui *adminUI) apiKeyCreate(w http.ResponseWriter, r *http.Request) {
	keys, err := ui.apiKeys.ListAPIKeys(r.Context())
	if err != nil {
		slog.Error("api key create: list api keys", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		ui.renderAPIKeys(w, r, http.StatusBadRequest, keys, 1, apiKeyForm{Error: "Invalid form submission."})
		return
	}
	req := createAPIKeyRequest{Name: r.PostFormValue("name")}
	if err := req.validate(); err != nil {
		ui.renderAPIKeys(w, r, http.StatusUnprocessableEntity, keys, 1, apiKeyForm{Name: req.Name, Error: err.Error()})
		return
	}

	key, rawKey, err := issueAPIKey(r.Context(), ui.apiKeys, req.Name)
	if err != nil {
		slog.Error("api key create: create api key", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Keeps the page, and the key on it, out of the browser's cache.
	w.Header().Set("Cache-Control", "no-store")
	keys = append([]APIKey{*key}, keys...)
	ui.renderAPIKeys(w, r, http.StatusOK, keys, 1, apiKeyForm{NewKey: rawKey, NewKeyName: key.Name})
}

// apiKeyDeactivate revokes a key from its row on the list. It mirrors
// handleDeactivateAPIKey, except that an id that cannot be a key is a 404
// like an unknown one, as on the other admin pages. There is no way back:
// the API has no reactivate, so neither does the page.
func (ui *adminUI) apiKeyDeactivate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := ui.apiKeys.DeactivateAPIKey(r.Context(), id); err != nil {
		if errors.Is(err, ErrAPIKeyNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("api key deactivate: deactivate api key", "api_key_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	slog.Info("api key revoked", "api_key_id", id, "admin_sub", adminSub(r))
	setFlash(w, "api_key_deactivated")
	http.Redirect(w, r, "/admin/api-keys", http.StatusSeeOther)
}
