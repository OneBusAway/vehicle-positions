package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

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
		MaxAge:   int((24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsSecure(r, trustProxy),
	})
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

// adminClaimsFromCookie validates the session cookie's JWT and requires the
// admin role. It mirrors requireAuth's validation exactly (HS256, issuer).
func adminClaimsFromCookie(r *http.Request, secret []byte) (jwt.MapClaims, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	token, err := jwt.Parse(c.Value, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("vehicle-positions-api"))
	if err != nil || !token.Valid {
		return nil, false
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, false
	}
	if role, _ := claims["role"].(string); role != "admin" {
		return nil, false
	}
	return claims, true
}

// requireAdminPage guards HTML admin pages: unauthenticated or non-admin
// visitors are redirected to the login page (303) rather than given JSON.
func requireAdminPage(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := adminClaimsFromCookie(r, secret)
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
