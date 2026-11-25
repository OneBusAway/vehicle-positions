package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// ChangePasswordRequest is the JSON payload for POST /api/v1/auth/password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	// RefreshToken is the caller's own, the sign-in that goes on.
	RefreshToken string `json:"refresh_token"`
}

// handleChangePassword lets a driver set a new password, given the current one.
// Every other sign-in of theirs ends at once; the one whose refresh token they
// send goes on, with the same refresh token, so an answer lost on the way does
// not sign it out.
//
// A wrong current password is a 422: the apps read a 401 as a sign-in that is
// over. Guesses at it use up the account's and the address's login budget, so a
// phone left signed in is no way round the login's limit.
func handleChangePassword(store OwnPasswordChanger, limiter *LoginRateLimiter, trustProxy bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}
		userID, claimsErr, status := userIDFromClaims(r)
		if claimsErr != nil {
			slog.Warn("handleChangePassword: invalid claims", "error", claimsErr)
			writeJSON(w, status, map[string]string{"error": claimsErr.Error()})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
		var req ChangePasswordRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + sanitizeJSONError(err)})
			return
		}
		if err := decoder.Decode(new(json.RawMessage)); err == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: request body must contain a single JSON object and no trailing data"})
			return
		} else if err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + sanitizeJSONError(err)})
			return
		}
		if req.CurrentPassword == "" || req.NewPassword == "" || req.RefreshToken == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "current_password, new_password and refresh_token are required"})
			return
		}
		if err := validatePassword(req.NewPassword); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new_" + err.Error()})
			return
		}

		email, hash, err := store.GetUserCredentials(r.Context(), userID)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
				return
			}
			slog.Error("change password: database error", "sub", userID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		ip := clientIP(r, trustProxy)
		if limiter != nil && !limiter.Allow(ip, email) {
			slog.Warn("change password: too many attempts", "sub", userID, "ip", ip)
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts"})
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.CurrentPassword)); err != nil {
			if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
				writeWrongCurrentPassword(w, userID, ip)
				return
			}
			slog.Error("change password: bcrypt error", "sub", userID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		if limiter != nil {
			limiter.RecordSuccess(ip, email)
		}

		claims, _ := r.Context().Value(claimsKey).(jwt.MapClaims)
		r, err = withAuditActor(r, claims, trustProxy)
		if err != nil {
			slog.Error("change password: audit actor", "sub", userID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		err = store.ChangeOwnPassword(r.Context(), passwordChange{
			UserID: userID, CheckedHash: hash, NewPassword: req.NewPassword,
			KeepTokenHash: hashRefreshToken(req.RefreshToken),
		})
		switch {
		case errors.Is(err, ErrPasswordChangedMeanwhile):
			writeWrongCurrentPassword(w, userID, ip)
		case errors.Is(err, ErrRefreshTokenNotFound):
			slog.Warn("change password: refresh token is not a live one of the account's", "sub", userID, "ip", ip)
			writeJSON(w, http.StatusConflict, map[string]string{"error": "refresh_token is not a current one of this account's"})
		case errors.Is(err, ErrUserNotFound):
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
		case err != nil:
			slog.Error("change password failed", "sub", userID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		default:
			slog.Info("password changed by its account, other sign-ins ended", "sub", userID, "ip", ip)
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func writeWrongCurrentPassword(w http.ResponseWriter, userID int64, ip string) {
	slog.Warn("change password: wrong current password", "sub", userID, "ip", ip)
	writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "current password is wrong"})
}
