package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

type CreateUserRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type UpdateUserRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
	// Password, when non-empty, replaces the user's password. Blank keeps
	// the current one â€” the same contract as the admin UI's edit form.
	Password string `json:"password"`
	// Active, when non-nil, toggles the user's active flag. Deactivation
	// revokes every refresh token the user holds. Omitting the field
	// leaves the flag unchanged.
	Active *bool `json:"active,omitempty"`
}

// handleListUsers returns one page of users, newest first, bounded by the
// limit/offset query params and narrowed by the optional q and role filters.
// Deactivated users are included, as they always have been. The response
// stays a bare JSON array rather than a paging envelope so existing clients
// keep working.
func handleListUsers(store UserPager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset, ok := parseListPageParams(w, r)
		if !ok {
			return
		}

		query := r.URL.Query()
		q := query.Get("q")
		if err := validateListQuery(q); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		role := query.Get("role")
		if !validUserRoleFilter(role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": `role must be "", "driver", or "admin"`})
			return
		}

		users, err := store.ListUsersPage(r.Context(), UserFilter{
			Role:   role,
			Q:      q,
			Limit:  int32(limit),
			Offset: int32(offset),
		})
		if err != nil {
			slog.Error("failed to list users", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, users)
	}
}

func handleGetUser(store UserGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUserID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
			return
		}

		user, err := store.GetUser(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
				return
			}
			slog.Error("failed to get user", "id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		writeJSON(w, http.StatusOK, user)
	}
}

func handleCreateUser(store UserCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		var req CreateUserRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if err := decoder.Decode(new(json.RawMessage)); err == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: request body must contain a single JSON object and no trailing data"})
			return
		} else if err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}

		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		if req.Email == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
			return
		}
		if req.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password is required"})
			return
		}
		if err := validatePassword(req.Password); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !validUserRole(req.Role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be 'driver' or 'admin'"})
			return
		}

		user, err := store.CreateUser(r.Context(), req.Name, req.Email, req.Password, req.Role)
		if err != nil {
			if errors.Is(err, ErrDuplicateEmail) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "email already exists"})
				return
			}
			slog.Error("failed to create user", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		writeJSON(w, http.StatusCreated, user)
	}
}

func handleUpdateUser(store UserUpdater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}

		id, err := parseUserID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		var req UpdateUserRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if err := decoder.Decode(new(json.RawMessage)); err == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: request body must contain a single JSON object and no trailing data"})
			return
		} else if err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}

		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		if req.Email == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
			return
		}
		if !validUserRole(req.Role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be 'driver' or 'admin'"})
			return
		}
		if req.Password != "" {
			if err := validatePassword(req.Password); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}

		// ── Load the original user BEFORE any writes ──────────────────
		// The last-admin guard needs the pre-update state. Reading after
		// UpdateUser would see the new role, letting an admin demote
		// themselves to driver AND deactivate in the same request —
		// bypassing the guard entirely. A 422 returned after UpdateUser
		// would also leave behind partial profile/password changes.
		original, err := store.GetUser(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
				return
			}
			slog.Error("failed to get user before update", "id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// ── Validate role/active changes against the ORIGINAL state ────
		// Determine the effective role and active flag after the proposed
		// update so we can check whether the result would leave zero
		// active admins. This catches deactivation, role demotion, and
		// the combination of both in a single request.
		newRole := req.Role
		newActive := original.Active
		if req.Active != nil {
			newActive = *req.Active
		}

		wasActiveAdmin := original.Role == roleAdmin && original.Active
		willBeActiveAdmin := newRole == roleAdmin && newActive
		if wasActiveAdmin && !willBeActiveAdmin {
			admins, err := store.CountActiveUsersByRole(r.Context(), roleAdmin)
			if err != nil {
				slog.Error("failed to count active admins", "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			if admins <= 1 {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "cannot deactivate the last active admin"})
				return
			}
		}

		// ── All validation passed — now commit writes ──────────────────
		user, err := store.UpdateUser(r.Context(), id, req.Name, req.Email, req.Role)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
				return
			}
			if errors.Is(err, ErrDuplicateEmail) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "email already exists"})
				return
			}
			slog.Error("failed to update user", "id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		if req.Password != "" {
			// A password reset is how an admin responds to a stolen phone, so
			// it has to end the sessions that phone holds. The access token
			// still runs out its own clock, but without this the thief's
			// refresh token renews indefinitely and the reset accomplishes
			// nothing. Reported as a failure rather than logged and ignored:
			// an admin who sees success must not be left believing the
			// account is secured when it is not.
			if err := store.SetUserPasswordAndRevokeSessions(r.Context(), id, req.Password); err != nil {
				if errors.Is(err, ErrUserNotFound) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
					return
				}
				slog.Error("failed to change user password and revoke refresh tokens", "id", id, "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
		}

		if req.Active != nil {
			if err := store.SetUserActiveAndRevokeSessions(r.Context(), id, *req.Active); err != nil {
				if errors.Is(err, ErrUserNotFound) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
					return
				}
				slog.Error("failed to set user active", "id", id, "active", *req.Active, "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			user.Active = *req.Active
		}

		writeJSON(w, http.StatusOK, user)
	}
}

func handleDeleteUser(store UserDeleter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUserID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
			return
		}

		if err := store.DeleteUser(r.Context(), id); err != nil {
			if errors.Is(err, ErrUserNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
				return
			}
			if errors.Is(err, ErrUserHasTrips) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot delete a user who has trips; deactivate the user instead"})
				return
			}
			slog.Error("failed to delete user", "id", id, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func parseUserID(r *http.Request) (int64, error) {
	idStr := r.PathValue("id")
	if idStr == "" {
		return 0, errors.New("missing id")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}
