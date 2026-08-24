package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed web/templates web/static
var files embed.FS

// appStore is the combined store interface required by newMux and newHandler.
// *Store implements all embedded interfaces.
type appStore interface {
	UserFetcher
	UserLister
	UserGetter
	UserCreator
	UserUpdater
	UserDeleter
	UserActivator
	UserRoleCounter
	VehicleManager
	VehicleInfoUpdater
	VehicleActivator
	LocationSaver
	AssignmentCreator
	AssignmentDeleter
	AssignmentListerByUser
	AssignmentListerByVehicle
	TripStarter
	TripEnder
	TripLister
	TripSummaryGetter
	TripLocationLister
	ActiveTripsByVehicleLister
	HealthChecker
	LocationHistoryLister
	VehicleChecker
	DriverVehicleLister
	AdminStatsCounter
}

// newMux wires all application routes and returns the configured ServeMux.
// Extracting route registration here allows tests to build the real mux
// without a live database, catching middleware wiring gaps like the one fixed
// in issue #82.
func newMux(store appStore, tracker *Tracker, rateLimiter *VehicleRateLimiter, jwtSecret []byte, startTime time.Time, loginLimiter *LoginRateLimiter, trustProxy bool) *http.ServeMux {
	mux := http.NewServeMux()

	authMiddleware := requireAuth(jwtSecret)
	adminMiddleware := requireAdmin()

	mux.Handle("POST /api/v1/auth/login", handleLogin(store, jwtSecret, loginLimiter, trustProxy))
	mux.HandleFunc("GET /gtfs-rt/vehicle-positions", handleGetFeed(tracker))
	mux.Handle("GET /api/v1/admin/status", authMiddleware(adminMiddleware(handleAdminStatus(tracker, startTime))))
	mux.Handle("GET /api/v1/admin/vehicles", authMiddleware(adminMiddleware(handleListVehicles(store))))
	mux.Handle("GET /api/v1/admin/vehicles/live", authMiddleware(adminMiddleware(handleLiveVehicles(tracker, store, store))))
	mux.Handle("GET /api/v1/admin/vehicles/{id}", authMiddleware(adminMiddleware(handleGetVehicle(store))))
	mux.Handle("POST /api/v1/admin/vehicles", authMiddleware(adminMiddleware(handleUpsertVehicle(store))))
	mux.Handle("DELETE /api/v1/admin/vehicles/{id}", authMiddleware(adminMiddleware(handleDeactivateVehicle(store))))
	mux.Handle("GET /api/v1/admin/vehicles/{vehicleID}/locations", authMiddleware(adminMiddleware(handleGetLocationHistory(store, store))))
	mux.Handle("GET /api/v1/admin/trips", authMiddleware(adminMiddleware(handleListTrips(store))))
	mux.Handle("GET /api/v1/admin/trips/{id}/locations", authMiddleware(adminMiddleware(handleTripLocations(store))))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ready", handleReadiness(store))

	mux.Handle("POST /api/v1/locations", authMiddleware(handlePostLocation(store, tracker, rateLimiter)))
	mux.Handle("POST /api/v1/trips/start", authMiddleware(handleStartTrip(store)))
	mux.Handle("POST /api/v1/trips/end", authMiddleware(handleEndTrip(store)))
	mux.Handle("GET /api/v1/vehicles", authMiddleware(handleListMyVehicles(store)))

	// Admin user management
	mux.Handle("GET /api/v1/admin/users", authMiddleware(adminMiddleware(handleListUsers(store))))
	mux.Handle("GET /api/v1/admin/users/{id}", authMiddleware(adminMiddleware(handleGetUser(store))))
	mux.Handle("POST /api/v1/admin/users", authMiddleware(adminMiddleware(handleCreateUser(store))))
	mux.Handle("PUT /api/v1/admin/users/{id}", authMiddleware(adminMiddleware(handleUpdateUser(store))))
	mux.Handle("DELETE /api/v1/admin/users/{id}", authMiddleware(adminMiddleware(handleDeleteUser(store))))

	// Admin user-vehicle assignments
	mux.Handle("POST /api/v1/admin/assignments", authMiddleware(adminMiddleware(handleCreateAssignment(store))))
	mux.Handle("DELETE /api/v1/admin/users/{userID}/vehicles/{vehicleID}", authMiddleware(adminMiddleware(handleDeleteAssignment(store))))
	mux.Handle("GET /api/v1/admin/users/{id}/vehicles", authMiddleware(adminMiddleware(handleListUserVehicles(store))))
	mux.Handle("GET /api/v1/admin/vehicles/{id}/users", authMiddleware(adminMiddleware(handleListVehicleUsers(store))))

	return mux
}

// newHandler composes the full application handler: the JSON API mux, the
// optional server-rendered admin UI (mounted only when cfg.enabled), and
// CSRF protection wrapping the whole thing. It is the single place routes
// and cross-cutting middleware come together.
func newHandler(store appStore, tracker *Tracker, rateLimiter *VehicleRateLimiter,
	loginLimiter *LoginRateLimiter, jwtSecret []byte, startTime time.Time,
	cfg adminUIConfig) (http.Handler, error) {

	mux := newMux(store, tracker, rateLimiter, jwtSecret, startTime, loginLimiter, cfg.trustProxy)

	if cfg.enabled {
		ui, err := newAdminUI(store, tracker, jwtSecret, loginLimiter, cfg)
		if err != nil {
			return nil, fmt.Errorf("init admin UI: %w", err)
		}
		staticFiles, err := fs.Sub(files, "web/static")
		if err != nil {
			return nil, fmt.Errorf("prepare static files: %w", err)
		}
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))
		registerAdminUI(mux, ui)
	}

	// CSRF: rejects browser cross-origin non-safe requests; clients without
	// Sec-Fetch-Site/Origin headers (Retrofit, curl) are unaffected (spec §4.3).
	csrf := http.NewCrossOriginProtection()
	return csrf.Handler(mux), nil
}

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

	if be, bp := os.Getenv("ADMIN_BOOTSTRAP_EMAIL"), os.Getenv("ADMIN_BOOTSTRAP_PASSWORD"); be != "" && bp != "" {
		if err := bootstrapAdmin(ctx, store, be, bp); err != nil {
			slog.Error("admin bootstrap failed", "error", err)
			os.Exit(1)
		}
	}

	tracker := NewTracker(maxAge)
	defer tracker.Stop()

	rateLimiter := NewVehicleRateLimiter()
	defer rateLimiter.Stop()

	loginLimiter := NewLoginRateLimiter()
	defer loginLimiter.Stop()

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

	handler, err := newHandler(store, tracker, rateLimiter, loginLimiter, jwtSecret, startTime,
		adminUIConfig{enabled: adminUIEnabled(), trustProxy: trustProxyHeaders(), stalenessThreshold: maxAge})
	if err != nil {
		slog.Error("failed to build handler", "error", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      requestLogger(handler),
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
