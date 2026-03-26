package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	port := envOrDefault("PORT", "8080")
	databaseURL := envOrDefault("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/vehicle_positions?sslmode=disable")
	maxAge := envDurationOrDefault("STALENESS_THRESHOLD", 5*time.Minute)

	readTimeout := envDurationOrDefault("READ_TIMEOUT", 15*time.Second)
	writeTimeout := envDurationOrDefault("WRITE_TIMEOUT", 15*time.Second)
	idleTimeout := envDurationOrDefault("IDLE_TIMEOUT", 60*time.Second)

	jwtSecretStr := os.Getenv("JWT_SECRET")
	if jwtSecretStr == "" {
		slog.Error("JWT_SECRET environment variable is not set")
		os.Exit(1)
	}
	if len(jwtSecretStr) < 32 {
		slog.Error("JWT_SECRET must be at least 32 bytes long for HMAC-SHA256 security")
		os.Exit(1)
	}
	jwtSecret := []byte(jwtSecretStr)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	store, err := NewStore(ctx, databaseURL)
	if err != nil {
		slog.Error("failed to initialize store", "error", err)
		os.Exit(1)
	}

	if err := store.Migrate(databaseURL); err != nil {
		slog.Error("could not run migrations", "error", err)
		os.Exit(1)
	}

	defer store.Close()

	// TODO: Initialize audit logging system here once the admin user CRUD endpoints are merged.
	// Example:
	// auditLogger, err := audit.NewAuditLogger(store)
	// if err != nil {
	// 	slog.Error("failed to initialize audit logger", "error", err)
	// 	os.Exit(1)
	// }
	// defer auditLogger.Close()

	// TODO: Initialize admin middleware or audit middleware once the admin user CRUD endpoints are merged.
	// Example:
	// adminMiddleware := audit.NewAdminMiddleware(auditLogger)
	// mux.Handle("/api/v1/admin/users", createUser(store, auditStore)).Methods("POST")
	// mux.Handle("/api/v1/admin/users/{id}", updateUser(store, auditStore)).Methods("PUT")
	// mux.Handle("/api/v1/admin/users/{id}", deleteUser(store, auditStore)).Methods("DELETE")
	// mux.Handle("/api/v1/admin/vehicles/{id}", deleteVehicle(store, auditStore)).Methods("DELETE")

	tracker := NewTracker(maxAge)
	defer tracker.Stop()

	rateLimiter := NewVehicleRateLimiter()
	defer rateLimiter.Stop()

	cutoff := time.Now().Add(-maxAge)
	recentLocations, err := store.GetRecentLocations(ctx, cutoff)
	if err != nil {
		slog.Warn("failed to seed tracker from database", "error", err)
	} else {
		for _, loc := range recentLocations {
			tracker.Update(loc)
		}
		slog.Info("seeded tracker", "active_vehicles", len(recentLocations))
	}

	startTime := time.Now()

	mux := http.NewServeMux()

	authMiddleware := requireAuth(jwtSecret)
	adminMiddleware := requireAdmin()

	mux.Handle("POST /api/v1/auth/login", handleLogin(store, jwtSecret))
	mux.HandleFunc("GET /gtfs-rt/vehicle-positions", handleGetFeed(tracker))
	mux.Handle("GET /api/v1/admin/status", authMiddleware(adminMiddleware(handleAdminStatus(tracker, startTime))))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ready", handleReadiness(store))

	mux.Handle("POST /api/v1/locations", authMiddleware(handlePostLocation(store, tracker, rateLimiter)))

	// Admin user management
	mux.Handle("GET /api/v1/admin/users", authMiddleware(handleListUsers(store)))
	mux.Handle("GET /api/v1/admin/users/{id}", authMiddleware(handleGetUser(store)))
	mux.Handle("POST /api/v1/admin/users", authMiddleware(handleCreateUser(store)))
	mux.Handle("PUT /api/v1/admin/users/{id}", authMiddleware(handleUpdateUser(store)))
	mux.Handle("DELETE /api/v1/admin/users/{id}", authMiddleware(handleDeactivateUser(store)))

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      requestLogger(mux),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	go func() {
		slog.Info("starting server", "port", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDurationOrDefault(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			slog.Warn("invalid duration, using default", "key", key, "value", v, "default", fallback)
			return fallback
		}
		return d
	}
	return fallback
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", float64(time.Since(start).Microseconds())/1000.0,
		)
	})
}
