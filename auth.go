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
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const claimsKey contextKey = "claims"

const bcryptCost = bcrypt.DefaultCost

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

// TokenChecker can verify whether a JWT has been revoked.
type TokenChecker interface {
	IsTokenRevoked(ctx context.Context, jti string) (bool, error)
}

// TokenRevoker can invalidate a JWT before its natural expiry.
type TokenRevoker interface {
	RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error
}

func handleLogin(fetcher UserFetcher, secret []byte) http.HandlerFunc {
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

		user, err := fetcher.GetUserByEmail(r.Context(), req.Email)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
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

		tokenStr, err := generateJWT(user, secret)
		if err != nil {
			slog.Error("login: failed to generate JWT", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		writeJSON(w, http.StatusOK, LoginResponse{Token: tokenStr})
	}
}

// generateJWT creates a signed JWT valid for 24 hours.
// A unique jti (JWT ID) claim is included so the token can be individually revoked.
func generateJWT(user *User, secret []byte) (string, error) {
	now := time.Now()

	claims := jwt.MapClaims{
		"sub":   fmt.Sprintf("%d", user.ID),
		"email": user.Email,
		"role":  user.Role,
		"exp":   now.Add(24 * time.Hour).Unix(),
		"iat":   now.Unix(),
		"iss":   "vehicle-positions-api",
		"jti":   uuid.New().String(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// requireAuth is middleware that validates the Bearer JWT on protected routes.
// It checks the token signature, expiry, and whether the jti has been revoked.
func requireAuth(secret []byte, checker TokenChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
				return secret, nil
			}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("vehicle-positions-api"))

			if err != nil || !token.Valid {
				slog.Warn("auth: token validation failed", "error", err)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token claims"})
				return
			}

			if jti, ok := claims["jti"].(string); ok && jti != "" {
				revoked, err := checker.IsTokenRevoked(r.Context(), jti)
				if err != nil {
					slog.Error("auth: revocation check failed", "error", err)
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
					return
				}
				if revoked {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token has been revoked"})
					return
				}
			}

			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func handleLogout(revoker TokenRevoker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(claimsKey).(jwt.MapClaims)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		jti, ok := claims["jti"].(string)
		if !ok || jti == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token missing jti claim"})
			return
		}

		expFloat, ok := claims["exp"].(float64)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token missing exp claim"})
			return
		}
		expiresAt := time.Unix(int64(expFloat), 0)

		if err := revoker.RevokeToken(r.Context(), jti, expiresAt); err != nil {
			slog.Error("logout: failed to revoke token", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"message": "logged out successfully"})
	}
}
