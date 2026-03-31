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
}

func handleListUsers(store UserLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := store.ListUsers(r.Context())
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
		if len(req.Password) < 8 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
			return
		}
		if req.Role != "driver" && req.Role != "admin" {
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
		if req.Role != "driver" && req.Role != "admin" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be 'driver' or 'admin'"})
			return
		}

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
