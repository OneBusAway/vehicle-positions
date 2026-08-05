package main

import (
	"log/slog"
	"net/http"
)

// handleListMyVehicles returns the calling driver's assigned, active vehicles.
// Unlike the admin listing (handleListUserVehicles), the user ID comes from the
// JWT claims, so any authenticated driver can call it — no admin role required.
func handleListMyVehicles(store DriverVehicleLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, claimsErr, status := userIDFromClaims(r)
		if claimsErr != nil {
			slog.Warn("handleListMyVehicles: invalid claims", "error", claimsErr)
			writeJSON(w, status, map[string]string{"error": claimsErr.Error()})
			return
		}

		vehicles, err := store.ListActiveVehiclesByUser(r.Context(), userID)
		if err != nil {
			slog.Error("failed to list driver vehicles", "user_id", userID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		if vehicles == nil {
			vehicles = []VehicleResponse{}
		}

		writeJSON(w, http.StatusOK, vehicles)
	}
}
