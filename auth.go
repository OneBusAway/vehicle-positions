package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type RefreshResponse struct {
	AccessToken string `json:"access_token"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// UserFetcher is the store interface needed by the login handler.
type UserFetcher interface {
	GetUserByEmail(ctx context.Context, email string) (*User, error)
}

type RefreshTokenCreator interface {
	CreateRefreshToken(ctx context.Context, id string, userID int64, tokenHash string, issuedAt time.Time, expiresAt time.Time) error
}

type LoginStore interface {
	UserFetcher
	RefreshTokenCreator
}

type RefreshTokenGetter interface {
	GetRefreshToken(ctx context.Context, tokenHash string) (*RefreshToken, error)
}

type UserByIDFetcher interface {
	GetUserByID(ctx context.Context, userID int64) (*User, error)
}

type RefreshStore interface {
	RefreshTokenGetter
	UserByIDFetcher
}

func handleLogin(deps LoginStore, secret []byte, accessTTL, refreshTTL time.Duration) http.HandlerFunc {
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

		user, err := deps.GetUserByEmail(r.Context(), req.Email)
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

		accessToken, err := generateJWT(user, secret, accessTTL)
		if err != nil {
			log.Printf("login: failed to generate JWT: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		rawToken, tokenHash, err := generateRefreshToken()
		if err != nil {
			log.Printf("login: failed to generate refresh token: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		now := time.Now()
		if err := deps.CreateRefreshToken(r.Context(), uuid.New().String(), user.ID, tokenHash, now, now.Add(refreshTTL)); err != nil {
			log.Printf("login: failed to store refresh token: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		writeJSON(w, http.StatusOK, LoginResponse{AccessToken: accessToken, RefreshToken: rawToken})
	}
}

func generateRefreshToken() (token string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(token))
	hash = fmt.Sprintf("%x", sum)
	return token, hash, nil
}

func handleRefreshToken(deps RefreshStore, secret []byte, accessTTL time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		var req RefreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.RefreshToken == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "refresh_token is required"})
			return
		}

		sum := sha256.Sum256([]byte(req.RefreshToken))
		tokenHash := fmt.Sprintf("%x", sum)

		rt, err := deps.GetRefreshToken(r.Context(), tokenHash)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid refresh token"})
				return
			}
			log.Printf("refresh: database error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		if rt.Revoked {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "refresh token has been revoked"})
			return
		}

		if time.Now().After(rt.ExpiresAt) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "refresh token has expired"})
			return
		}

		user, err := deps.GetUserByID(r.Context(), rt.UserID)
		if err != nil {
			log.Printf("refresh: failed to fetch user: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		accessToken, err := generateJWT(user, secret, accessTTL)
		if err != nil {
			log.Printf("refresh: failed to generate JWT: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		writeJSON(w, http.StatusOK, RefreshResponse{AccessToken: accessToken})
	}
}

func generateJWT(user *User, secret []byte, ttl time.Duration) (string, error) {
	now := time.Now()

	claims := jwt.MapClaims{
		"sub":   fmt.Sprintf("%d", user.ID),
		"email": user.Email,
		"role":  user.Role,
		"exp":   now.Add(ttl).Unix(),
		"iat":   now.Unix(),
		"iss":   "vehicle-positions-api",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
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
