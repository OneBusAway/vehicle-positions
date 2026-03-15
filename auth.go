package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
			log.Printf("login: database error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password))
		if err != nil {
			if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
				return
			}
			log.Printf("login: bcrypt error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		tokenStr, err := generateJWT(user, secret)
		if err != nil {
			log.Printf("login: failed to generate JWT: %v", err)
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

// requireAuth is middleware that validates the Bearer JWT on protected routes.
func requireAuth(secret []byte) func(http.Handler) http.Handler {
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
				log.Printf("auth: token validation failed: %v", err)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token claims"})
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
