package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const claimsKey contextKey = "claims"

// contextWithClaims stores validated JWT claims on the context. Shared by
// requireAuth and requireAdminPage so both middlewares wire claims the same
// way.
func contextWithClaims(ctx context.Context, claims jwt.MapClaims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

const bcryptCost = bcrypt.DefaultCost

// sessionCookieName is the cookie holding the admin UI's browser session
// JWT. requireAuth falls back to it only when the Authorization header is
// entirely absent (see requireAuth). Task 7's session helpers reuse it.
const sessionCookieName = "vp_session"

var dummyHash []byte

func init() {
	// Generate a valid hash at startup using the central cost.
	// This ensures our timing side-channel prevention always matches the real hashing time.
	var err error
	dummyHash, err = bcrypt.GenerateFromPassword([]byte("dummy"), bcryptCost)
	if err != nil {
		panic("failed to generate dummy hash at startup: " + err.Error())
	}
}

// LoginRequest is the JSON payload for POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is returned on a successful login.
type LoginResponse struct {
	Token string `json:"token"`
}

// UserFetcher is the store interface needed by the login handler.
type UserFetcher interface {
	GetUserByEmail(ctx context.Context, email string) (*User, error)
}

// handleLogin returns the JSON API login handler. limiter may be nil (e.g. in
// tests that don't exercise rate limiting); trustProxy controls which IP
// clientIP() reports to the limiter. When present, the rate-limit check runs
// before the store is touched.
func handleLogin(fetcher UserFetcher, secret []byte, limiter *LoginRateLimiter, trustProxy bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		var req LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.Email == "" || req.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and password are required"})
			return
		}

		if limiter != nil && !limiter.Allow(clientIP(r, trustProxy), req.Email) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts"})
			return
		}

		user, err := fetcher.GetUserByEmail(r.Context(), req.Email)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password)) // timing side-channel prevention
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
				return
			}
			slog.Error("login: database error", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password))
		if err != nil {
			if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
				return
			}
			slog.Error("login: bcrypt error", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		if !user.Active {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
			return
		}

		tokenStr, err := generateJWT(user, secret)
		if err != nil {
			slog.Error("token generation failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		writeJSON(w, http.StatusOK, LoginResponse{Token: tokenStr})
	}
}

// generateJWT creates a signed JWT valid for 24 hours.
func generateJWT(user *User, secret []byte) (string, error) {
	now := time.Now()

	claims := jwt.MapClaims{
		"sub":   fmt.Sprintf("%d", user.ID),
		"email": user.Email,
		"role":  user.Role,
		"exp":   now.Add(24 * time.Hour).Unix(),
		"iat":   now.Unix(),
		"iss":   "vehicle-positions-api",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// requireAdmin is middleware that restricts access to admin-role users.
// It must be chained after requireAuth, which sets JWT claims on the context.
func requireAdmin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := r.Context().Value(claimsKey).(jwt.MapClaims)
			if !ok {
				slog.Warn("requireAdmin: claims missing from context")
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}

			role, ok := claims["role"].(string)
			if !ok || role != "admin" {
				slog.Warn("requireAdmin: access denied",
					"sub", claims["sub"],
					"role", claims["role"],
					"path", r.URL.Path,
				)
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// parseSessionToken validates an HS256 session JWT (algorithm, issuer) and
// returns its claims. It is the single validation path shared by the API
// middleware and the admin UI's cookie session (adminClaimsFromCookie), so
// changes to token validation cannot silently diverge between the two.
func parseSessionToken(tokenString string, secret []byte) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("vehicle-positions-api"))
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("token marked invalid")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}

// requireAuth is middleware that validates the Bearer JWT on protected routes.
func requireAuth(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			var tokenString string
			switch {
			case authHeader == "":
				// Cookie fallback for the admin UI's browser session
				// (spec §4.2). Applies ONLY when the header is entirely
				// absent — a present-but-bad header never falls back.
				c, err := r.Cookie(sessionCookieName)
				if err != nil || c.Value == "" {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
					return
				}
				tokenString = c.Value
			case strings.HasPrefix(authHeader, "Bearer "):
				tokenString = strings.TrimPrefix(authHeader, "Bearer ")
			default:
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
				return
			}

			claims, err := parseSessionToken(tokenString, secret)
			if err != nil {
				slog.Warn("token validation failed", "error", err)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
				return
			}
			ctx := contextWithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
