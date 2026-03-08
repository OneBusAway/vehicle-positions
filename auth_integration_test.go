package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAuthFlow_Integration(t *testing.T) {
	// 1. Setup Mock Store (or use real DB if preferred, but mock is safer for CI)
	// For this test, we'll use a real store if DATABASE_URL is set, otherwise skip
	dbURL := "postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable"
	store, err := NewStore(t.Context(), dbURL)
	if err != nil {
		t.Skip("Skipping integration test; database not available:", err)
	}
	if err := store.Migrate(dbURL); err != nil {
		t.Errorf("failed to run migrations in test: %v", err)
	}
	defer store.Close()

	tracker := NewTracker(5 * time.Minute)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", handleLogin(store))
	mux.HandleFunc("POST /api/v1/admin/drivers", handleAdminCreateDriver(store))
	mux.HandleFunc("POST /api/v1/locations", AuthMiddleware(handlePostLocation(store, tracker)))

	phone := fmt.Sprintf("+12345%d", time.Now().Unix())
	pin := "9999"
	name := "Test Driver"

	// 2. Create Driver via Admin API
	adminReq, _ := json.Marshal(AdminCreateDriverRequest{
		Phone: phone,
		PIN:   pin,
		Name:  name,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/admin/drivers", bytes.NewBuffer(adminReq))
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusCreated, w.Code)

	// 3. Login
	loginReq, _ := json.Marshal(LoginRequest{
		Phone: phone,
		PIN:   pin,
	})
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBuffer(loginReq))
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code)

	var loginResp struct {
		Token string `json:"token"`
	}
	json.NewDecoder(w.Body).Decode(&loginResp)
	assert.NotEmpty(t, loginResp.Token)

	// 4. Post Location with Token
	locReq, _ := json.Marshal(LocationReport{
		VehicleID: "V-TEST-01",
		Latitude:  1.23,
		Longitude: 4.56,
		Timestamp: time.Now().Unix(),
	})
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/api/v1/locations", bytes.NewBuffer(locReq))
	r.Header.Set("Authorization", "Bearer "+loginResp.Token)
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusCreated, w.Code)

	// 5. Post Location without Token (Should fail)
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/api/v1/locations", bytes.NewBuffer(locReq))
	mux.ServeHTTP(w, r)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
