package main

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/golang-jwt/jwt/v5"
)

const flashCookieName = "vp_flash"

// flashMessages maps opaque flash codes to the fixed strings the layout
// renders. Cookie values are attacker-writable, so free text is never
// rendered — unknown codes yield nothing (spec §4.6).
var flashMessages = map[string]string{
	"vehicle_created":     "Vehicle created.",
	"vehicle_updated":     "Vehicle updated.",
	"vehicle_deactivated": "Vehicle deactivated.",
	"vehicle_activated":   "Vehicle reactivated.",
	"user_created":        "User created.",
	"user_updated":        "User updated.",
	"user_deactivated":    "User deactivated.",
	"user_activated":      "User reactivated.",
	"vehicle_assigned":    "Vehicle assigned.",
	"vehicle_unassigned":  "Vehicle unassigned.",
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, trustProxy bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(tokenLifetime.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsSecure(r, trustProxy),
	})
}

// revokeSessionCookie revokes the JWT carried by the session cookie, so
// signing out of the admin UI ends the token itself and not just the browser's
// copy of it. It is best-effort by design: the caller clears the cookie and
// redirects regardless, and a cookie that no longer parses has nothing to
// revoke. Errors are logged rather than returned for the same reason.
func revokeSessionCookie(r *http.Request, secret []byte, revoker TokenRevoker) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return
	}
	claims, err := parseSessionToken(c.Value, secret)
	if err != nil {
		slog.Debug("admin logout: session cookie no longer valid, nothing to revoke", "error", err)
		return
	}
	jti, _ := claims["jti"].(string)
	if jti == "" {
		// Pre-revocation token; see checkRevoked.
		slog.Warn("admin logout: session token has no jti, nothing to revoke", "sub", claims["sub"])
		return
	}
	sub, err := claims.GetSubject()
	if err != nil {
		slog.Warn("admin logout: unreadable sub claim", "error", err)
		return
	}
	userID, err := strconv.ParseInt(sub, 10, 64)
	if err != nil {
		slog.Warn("admin logout: sub claim is not a user ID", "sub", sub, "error", err)
		return
	}
	expiresAt, err := claims.GetExpirationTime()
	if err != nil || expiresAt == nil {
		slog.Warn("admin logout: unreadable exp claim", "sub", sub, "error", err)
		return
	}
	if err := revoker.RevokeToken(r.Context(), jti, userID, expiresAt.Time); err != nil {
		slog.Error("admin logout: failed to revoke session token", "sub", sub, "error", err)
		return
	}
	slog.Info("admin session token revoked", "sub", sub)
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// adminClaimsFromCookie validates the session cookie's JWT via the shared
// parseSessionToken path, rejects a revoked token via the shared checkRevoked
// path, and additionally requires the admin role.
//
// This is the browser half of the revocation enforcement in requireAuth: both
// carry the same JWT, so a token revoked through POST /api/v1/auth/logout must
// end the admin session too. A checker error is treated as "no session" —
// fail closed, matching requireAuth.
func adminClaimsFromCookie(r *http.Request, secret []byte, checker TokenChecker) (jwt.MapClaims, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	claims, err := parseSessionToken(c.Value, secret)
	if err != nil {
		return nil, false
	}
	revoked, err := checkRevoked(r.Context(), claims, checker)
	if err != nil {
		slog.Error("admin session: revocation check failed", "error", err, "path", r.URL.Path)
		return nil, false
	}
	if revoked {
		slog.Warn("admin session: rejected revoked token", "sub", claims["sub"], "path", r.URL.Path)
		return nil, false
	}
	if role, _ := claims["role"].(string); role != roleAdmin {
		return nil, false
	}
	return claims, true
}

// requireAdminPage guards HTML admin pages: unauthenticated or non-admin
// visitors are redirected to the login page (303) rather than given JSON.
func requireAdminPage(secret []byte, checker TokenChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := adminClaimsFromCookie(r, secret, checker)
			if !ok {
				http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
				return
			}
			ctx := contextWithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func setFlash(w http.ResponseWriter, code string) {
	http.SetCookie(w, &http.Cookie{
		Name: flashCookieName, Value: code, Path: "/", MaxAge: 60,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// takeFlash reads, clears, and resolves the flash cookie to its message.
func takeFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name: flashCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	msg, ok := flashMessages[c.Value]
	if !ok {
		slog.Debug("unknown flash code ignored", "code", c.Value)
		return ""
	}
	return msg
}
