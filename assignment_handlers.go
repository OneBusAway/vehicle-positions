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

// AssignmentRequest is the JSON payload for creating a user-vehicle assignment.
type AssignmentRequest struct {
	UserID    int64  `json:"user_id"`
	VehicleID string `json:"vehicle_id"`
}

func handleCreateAssignment(store AssignmentCreator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		var req AssignmentRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if err := decoder.Decode(new(json.RawMessage)); err == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: request body must contain a single JSON object and no trailing data"})
			return
		} else if !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}

		if req.UserID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required and must be positive"})
			return
		}
		if err := validateVehicleID(req.VehicleID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		assignment, err := store.CreateAssignment(r.Context(), req.UserID, req.VehicleID)
		if err != nil {
			if errors.Is(err, ErrAssignmentExists) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "assignment already exists"})
				return
			}
			if errors.Is(err, ErrUserNotFoundFK) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
				return
			}
			if errors.Is(err, ErrVehicleNotFoundFK) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "vehicle not found"})
				return
			}
			slog.Error("failed to create assignment", "user_id", req.UserID, "vehicle_id", req.VehicleID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		writeJSON(w, http.StatusCreated, assignment)
	}
}

func handleDeleteAssignment(store AssignmentDeleter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userIDStr := r.PathValue("userID")
		userID, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil || userID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
			return
		}

		vehicleID := r.PathValue("vehicleID")
		if err := validateVehicleID(vehicleID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid vehicle id"})
			return
		}

		err = store.DeleteAssignment(r.Context(), userID, vehicleID)
		if err != nil {
			if errors.Is(err, ErrAssignmentNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "assignment not found"})
				return
			}
			slog.Error("failed to delete assignment", "user_id", userID, "vehicle_id", vehicleID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "assignment removed"})
	}
}

func handleListUserVehicles(store AssignmentListerByUser) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		userID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || userID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
			return
		}

		assignments, err := store.ListAssignmentsByUser(r.Context(), userID)
		if err != nil {
			slog.Error("failed to list assignments by user", "user_id", userID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		if assignments == nil {
			assignments = []AssignmentResponse{}
		}

		writeJSON(w, http.StatusOK, assignments)
	}
}

func handleListVehicleUsers(store AssignmentListerByVehicle) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vehicleID := r.PathValue("id")
		if err := validateVehicleID(vehicleID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid vehicle id"})
			return
		}

		assignments, err := store.ListAssignmentsByVehicle(r.Context(), vehicleID)
		if err != nil {
			slog.Error("failed to list assignments by vehicle", "vehicle_id", vehicleID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		if assignments == nil {
			assignments = []AssignmentResponse{}
		}

		writeJSON(w, http.StatusOK, assignments)
	}
}
