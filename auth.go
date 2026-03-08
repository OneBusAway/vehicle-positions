package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
)

var jwtKey = []byte(envOrDefault("JWT_SECRET", "super-secret-key-change-me"))

type Claims struct {
	DriverID string `json:"driver_id"`
	Phone    string `json:"phone"`
	jwt.RegisteredClaims
}

// GenerateToken creates a new JWT for a driver.
func GenerateToken(driverID, phone string) (string, error) {
	expirationTime := time.Now().Add(24 * time.Hour)
	claims := &Claims{
		DriverID: driverID,
		Phone:    phone,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtKey)
}

// AuthMiddleware protects routes by requiring a valid JWT.
func AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization header required"})
			return
		}

		bearerToken := strings.Split(authHeader, " ")
		if len(bearerToken) != 2 || strings.ToLower(bearerToken[0]) != "bearer" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid authorization header format"})
			return
		}

		tokenString := bearerToken[1]
		claims := &Claims{}

		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		})

		if err != nil || !token.Valid {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
			return
		}

		// Inject driver ID into context
		ctx := context.WithValue(r.Context(), "driver_id", claims.DriverID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// Driver represents a driver entity in the system.
type Driver struct {
	ID      string
	Phone   string
	PINHash string
	Name    string
	Active  bool
}

// GetDriverByPhone retrieves a driver by their phone number for authentication.
func (s *Store) GetDriverByPhone(ctx context.Context, phone string) (*Driver, error) {
	var d Driver
	err := s.pool.QueryRow(ctx,
		"SELECT id, phone, pin_hash, name, active FROM drivers WHERE phone = $1",
		phone,
	).Scan(&d.ID, &d.Phone, &d.PINHash, &d.Name, &d.Active)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("driver not found")
		}
		return nil, fmt.Errorf("query driver: %w", err)
	}

	return &d, nil
}
