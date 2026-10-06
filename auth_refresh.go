package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"
)

// RefreshRequest is the JSON payload for POST /api/v1/auth/refresh.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// invalidRefreshTokenMessage is the single body returned for every rejected
// refresh token. Unknown, expired, already-used, and belonging-to-a-disabled-
// user must be indistinguishable to the caller: telling an attacker which one
// they hold turns a guess into information.
const invalidRefreshTokenMessage = "invalid or expired refresh token"

// issueRefreshToken mints a refresh token and stores its hash, returning the
// plaintext — the only moment the server holds a value it could not
// reconstruct.
func issueRefreshToken(ctx context.Context, creator RefreshTokenCreator, userID int64, ttl time.Duration) (string, error) {
	token, err := newRefreshTokenValue()
	if err != nil {
		return "", err
	}
	if err := creator.CreateRefreshToken(ctx, hashRefreshToken(token), userID, time.Now().Add(ttl)); err != nil {
		return "", err
	}
	return token, nil
}

// newLoginResponse builds the token payload shared by login and refresh. It
// is the one place Token is set, which is what keeps the deprecated alias and
// AccessToken from drifting apart.
func newLoginResponse(accessToken, refreshToken string, accessTTL time.Duration) LoginResponse {
	return LoginResponse{
		Token:        accessToken,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(accessTTL.Seconds()),
	}
}

// RefreshStore is what the refresh handler needs: read the presented token,
// look up its owner, rotate it, and revoke the owner's tokens when a spent one
// is presented.
type RefreshStore interface {
	RefreshTokenGetter
	RefreshTokenRotator
	RefreshTokenDeleter
	UserGetter
}

// handleRefreshToken exchanges a valid refresh token for a new access token
// and a new refresh token.
//
// The route is unauthenticated by necessity — the access token is expired by
// definition at the moment a client needs this, so the refresh token is the
// credential. That makes it as exposed as login, and it gets login's
// defenses: a 1KB body cap, strict JSON decoding, and the same rate limiter.
//
// Tokens are single-use. Each refresh consumes the presented token and
// returns its replacement, so a stolen token stops working as soon as the
// legitimate client refreshes. Presenting a token that is already spent
// revokes every refresh token its user holds, because the server cannot tell
// a client retrying a lost response from a thief replaying a stolen token.
func handleRefreshToken(store RefreshStore, secret []byte, ttls tokenTTLs, limiter *LoginRateLimiter, trustProxy bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		var req RefreshRequest
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

		if req.RefreshToken == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refresh_token is required"})
			return
		}

		ip := clientIP(r, trustProxy)

		// Only the IP dimension applies: the caller presents a token, not an
		// email, and there is nothing to key a per-account window on until
		// the lookup below. This shares the login limiter's per-IP budget by
		// design — one address cannot buy itself extra attempts by spreading
		// them across the two auth endpoints.
		if limiter != nil && !limiter.AllowIP(ip) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts"})
			return
		}

		stored, err := store.GetRefreshToken(r.Context(), hashRefreshToken(req.RefreshToken))
		if err != nil {
			if errors.Is(err, ErrRefreshTokenNotFound) {
				slog.Warn("refresh: unknown token presented", "ip", ip)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": invalidRefreshTokenMessage})
				return
			}
			slog.Error("refresh: database error", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		if stored.UsedAt != nil {
			// Either a client retrying a refresh whose response it lost, or a
			// stolen token being replayed. The two are indistinguishable from
			// here, so every refresh token the user holds is revoked: the
			// thief's copy dies along with the client's, and the client logs
			// in again. The caller still gets the same 401 as any other spent
			// token, so a thief cannot tell the replay was noticed.
			//
			// A failed revocation is a 500, not a 401. A 401 would send the
			// legitimate client to log in again while the thief's copy
			// survived. A 500 invites a retry, and retrying with this same
			// spent token attempts the revocation again.
			if err := store.DeleteRefreshTokensForUser(r.Context(), stored.UserID); err != nil {
				slog.Error("refresh: revoking refresh tokens after reuse failed",
					"sub", stored.UserID, "ip", ip, "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			slog.Warn("refresh: reuse of an already-consumed token, revoked the user's refresh tokens",
				"sub", stored.UserID, "used_at", *stored.UsedAt, "ip", ip)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": invalidRefreshTokenMessage})
			return
		}

		if !time.Now().Before(stored.ExpiresAt) {
			slog.Warn("refresh: expired token presented", "sub", stored.UserID, "expires_at", stored.ExpiresAt)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": invalidRefreshTokenMessage})
			return
		}

		user, err := store.GetUser(r.Context(), stored.UserID)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				slog.Warn("refresh: token belongs to a deleted user", "sub", stored.UserID)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": invalidRefreshTokenMessage})
				return
			}
			slog.Error("refresh: user lookup failed", "sub", stored.UserID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// A deactivated account must not be able to renew its way past
		// deactivation; handleLogin refuses the same user.
		if !user.Active {
			slog.Warn("refresh: token belongs to a deactivated user", "sub", stored.UserID)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": invalidRefreshTokenMessage})
			return
		}

		newRefresh, err := newRefreshTokenValue()
		if err != nil {
			slog.Error("refresh: token generation failed", "sub", stored.UserID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		rotated, err := store.RotateRefreshToken(r.Context(), stored.ID, hashRefreshToken(newRefresh),
			stored.UserID, time.Now().Add(ttls.refresh))
		if err != nil {
			slog.Error("refresh: rotation failed", "sub", stored.UserID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		if !rotated {
			// Another request consumed this token between the read above and
			// the update, so this is a spent token too and gets the same
			// revocation. Without it, a thief racing the legitimate client
			// could win the rotation: the client lands here and logs in again,
			// nobody ever replays the thief's new token, and it is never
			// revoked. RotateRefreshToken waited on the winner's user lock, so
			// the winner's replacement is committed and this delete removes it.
			if err := store.DeleteRefreshTokensForUser(r.Context(), stored.UserID); err != nil {
				slog.Error("refresh: revoking refresh tokens after a concurrent consume failed",
					"sub", stored.UserID, "ip", ip, "error", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			slog.Warn("refresh: token consumed concurrently, revoked the user's refresh tokens",
				"sub", stored.UserID, "ip", ip)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": invalidRefreshTokenMessage})
			return
		}

		accessToken, err := generateJWT(&User{ID: user.ID, Email: user.Email, Role: user.Role}, secret, ttls.access)
		if err != nil {
			// The old token is already spent, so this costs the client a
			// re-login. Nothing to roll back to: reinstating it would undo
			// single-use.
			slog.Error("refresh: access token generation failed", "sub", stored.UserID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		slog.Info("refresh token rotated", "sub", stored.UserID)
		writeJSON(w, http.StatusOK, newLoginResponse(accessToken, newRefresh, ttls.access))
	}
}
