package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestLogger_LogsFields(t *testing.T) {
	// Not safe for t.Parallel(); uses global logger
	var buf bytes.Buffer
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	handler := requestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest("POST", "/api/v1/locations", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var entry map[string]any
	err := json.Unmarshal(buf.Bytes(), &entry)
	require.NoError(t, err)

	assert.Equal(t, "request", entry["msg"])
	assert.Equal(t, "POST", entry["method"])
	assert.Equal(t, "/api/v1/locations", entry["path"])
	assert.Equal(t, float64(http.StatusCreated), entry["status"])
	assert.Contains(t, entry, "duration_ms")
}

func TestRequestLogger_DefaultStatus(t *testing.T) {
	// Not safe for t.Parallel(); uses global logger
	var buf bytes.Buffer
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	handler := requestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No explicit WriteHeader — defaults to 200
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	var entry map[string]any
	err := json.Unmarshal(buf.Bytes(), &entry)
	require.NoError(t, err)

	assert.Equal(t, float64(http.StatusOK), entry["status"])
}

func TestStatusRecorder_CapturesStatus(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

	rec.WriteHeader(http.StatusNotFound)

	assert.Equal(t, http.StatusNotFound, rec.status)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestEnvInt32OrDefault(t *testing.T) {
	// Not safe for t.Parallel(); uses t.Setenv and the global logger
	const key = "TEST_PRUNE_BATCH_SIZE"

	tests := []struct {
		name     string
		value    string
		set      bool
		expected int32
	}{
		{name: "valid", value: "5000", set: true, expected: 5000},
		{name: "unset", set: false, expected: 10_000},
		{name: "empty", value: "", set: true, expected: 10_000},
		{name: "non-numeric", value: "many", set: true, expected: 10_000},
		{name: "negative", value: "-1", set: true, expected: 10_000},
		{name: "zero", value: "0", set: true, expected: 10_000},
		{name: "exceeds int32", value: "2147483648", set: true, expected: 10_000},
		{name: "max int32", value: "2147483647", set: true, expected: 2147483647},
		{name: "float", value: "1.5", set: true, expected: 10_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(key, tt.value)
			}
			assert.Equal(t, tt.expected, envInt32OrDefault(key, 10_000))
		})
	}
}
