package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// metricsTokenMinLength is the shortest METRICS_TOKEN accepted, the same floor
// as JWT_SECRET: the token is the only thing between the internet and the
// endpoint, and nothing rate-limits guesses at it.
const metricsTokenMinLength = 32

// metricsContentType is version 0.0.4 of the Prometheus text exposition
// format, the one every Prometheus server and agent can read.
const metricsContentType = "text/plain; version=0.0.4; charset=utf-8"

// metricsTokenFromEnv reads METRICS_ENABLED and METRICS_TOKEN. It returns the
// token when /metrics is on and "" when it is off. On means a token is
// required: there is no unauthenticated mode, because whether the server's
// port is reachable only from the scraper depends on a firewall or proxy the
// server cannot see.
func metricsTokenFromEnv() (string, error) {
	enabled, err := envBool("METRICS_ENABLED", false)
	if err != nil {
		return "", err
	}
	if !enabled {
		return "", nil
	}
	token := os.Getenv("METRICS_TOKEN")
	if token == "" {
		return "", errors.New("METRICS_TOKEN must be set when METRICS_ENABLED is true")
	}
	if len(token) < metricsTokenMinLength {
		return "", fmt.Errorf("METRICS_TOKEN must be at least %d characters", metricsTokenMinLength)
	}
	return token, nil
}

// registerMetrics mounts GET /metrics when METRICS_ENABLED is true. Settings
// it cannot use leave the route off with an ERROR rather than stopping the
// server: off is the direction that exposes nothing, and the scraper reports
// the target down at its first scrape, so the mistake is seen at once without
// taking the feed down over a monitoring setting.
func registerMetrics(mux *http.ServeMux, trustProxy bool, catalog *gtfsCatalog, alerts *alertWatcher, health *feedHealth) {
	token, err := metricsTokenFromEnv()
	if err != nil {
		slog.Error("metrics disabled: invalid configuration", "error", err)
		return
	}
	if token == "" {
		return
	}
	mux.Handle("GET /metrics", requireMetricsToken(token, trustProxy)(handleMetrics(catalog, alerts, health)))
	slog.Info("metrics enabled at /metrics; scrapers must send the METRICS_TOKEN bearer token")
}

// requireMetricsToken admits a request carrying "Authorization: Bearer
// <token>". Both sides are hashed before the constant-time compare, so the
// comparison takes as long whatever the length of the guess.
func requireMetricsToken(token string, trustProxy bool) func(http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			reason := "no bearer token"
			if ok {
				sum := sha256.Sum256([]byte(got))
				if subtle.ConstantTimeCompare(sum[:], want[:]) == 1 {
					next.ServeHTTP(w, r)
					return
				}
				reason = "wrong token"
			}
			slog.Warn("metrics request refused", "reason", reason, "ip", clientIP(r, trustProxy))
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "a valid metrics token is required"})
		})
	}
}

// handleMetrics serves the counts the Health page shows, the schedule the
// Overview names and the open alerts, in the Prometheus text format. It is
// written by hand: the format is a few lines of text, and the counts already
// live in feedHealth, so the Prometheus client library would only add a
// dependency and a second registry to keep in step with the first.
//
// Only counts go out, under fixed labels and the build's Go version and
// commit. The GTFS source URL is left out, since an agency's feed URL can
// carry its API key. Working out the alerts reads the active and recent trips,
// the same two queries the Alerts page makes each time it reloads.
func handleMetrics(catalog *gtfsCatalog, alerts *alertWatcher, health *feedHealth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var out bytes.Buffer
		snap := health.snapshot()

		reports := func(p healthPeriod) []metricSample {
			return []metricSample{
				{metricLabel("outcome", "accepted"), p.Reports.Accepted},
				{metricLabel("outcome", "invalid"), p.Reports.Invalid},
				{metricLabel("outcome", "unauthorized"), p.Reports.Unauthorized},
				{metricLabel("outcome", "rate_limited"), p.Reports.RateLimited},
				{metricLabel("outcome", "server_error"), p.Reports.ServerError},
			}
		}
		fetches := func(p healthPeriod) []metricSample {
			return []metricSample{
				{metricLabel("outcome", "served"), p.Feed.Served},
				{metricLabel("outcome", "refused"), p.Feed.Refused},
				{metricLabel("outcome", "invalid"), p.Feed.Invalid},
				{metricLabel("outcome", "server_error"), p.Feed.ServerError},
			}
		}
		writeMetric(&out, "vehicle_positions_reports_total", "counter",
			"Position reports from driver phones since the server started, by outcome.", reports(snap.Total)...)
		writeMetric(&out, "vehicle_positions_reports_last_hour", "gauge",
			"Position reports from driver phones over the last hour, by outcome, as the Health page counts them.", reports(snap.LastHour)...)
		writeMetric(&out, "vehicle_positions_feed_fetches_total", "counter",
			"Fetches of the GTFS-RT feed since the server started, by outcome.", fetches(snap.Total)...)
		writeMetric(&out, "vehicle_positions_feed_fetches_last_hour", "gauge",
			"Fetches of the GTFS-RT feed over the last hour, by outcome, as the Health page counts them.", fetches(snap.LastHour)...)
		writeMetric(&out, "vehicle_positions_vehicles_reporting", "gauge",
			"Vehicles with a live report accepted in the last full minute, the Health page's Vehicles reporting tile.",
			metricSample{value: uint64(newHealthSummary(snap).Vehicles)})

		if ix := scheduleOf(catalog); ix != nil {
			stats := ix.Stats()
			writeMetric(&out, "vehicle_positions_gtfs_loaded_timestamp_seconds", "gauge",
				"When the GTFS schedule in use was downloaded, in Unix seconds.", metricSample{value: uint64(stats.LoadedAt.Unix())})
			writeMetric(&out, "vehicle_positions_gtfs_routes", "gauge",
				"Routes in the GTFS schedule in use.", metricSample{value: uint64(stats.Routes)})
			writeMetric(&out, "vehicle_positions_gtfs_trips", "gauge",
				"Trips in the GTFS schedule in use.", metricSample{value: uint64(stats.Trips)})
		}

		listed := uint64(1)
		open, err := alerts.list(r.Context(), time.Now())
		if err != nil {
			// The rest of the scrape still goes out: failing it would read as
			// the whole server down when only the database is.
			slog.Error("metrics: failed to list alerts", "error", err)
			listed = 0
		} else {
			writeMetric(&out, "vehicle_positions_alerts", "gauge",
				"Open alerts, the ones the Alerts page lists, by kind.", alertCounts(open)...)
		}
		writeMetric(&out, "vehicle_positions_alerts_listed", "gauge",
			"1 when the open alerts were worked out for this scrape, 0 when they could not be and vehicle_positions_alerts is left out.",
			metricSample{value: listed})

		writeMetric(&out, "vehicle_positions_build_info", "gauge",
			"Always 1, labelled with the Go version the server was built with and its source revision.",
			metricSample{labels: buildInfoLabels(), value: 1})

		w.Header().Set("Content-Type", metricsContentType)
		_, _ = w.Write(out.Bytes())
	}
}

// scheduleOf is the schedule in force, nil when there is none.
func scheduleOf(catalog *gtfsCatalog) *rider.Index {
	if catalog == nil {
		return nil
	}
	return catalog.index()
}

// alertCounts counts the open alerts by kind. Every kind the server raises is
// listed, at zero when none is open, so a query for a kind finds 0 rather than
// no data; a kind not in that list still counts under its own name.
func alertCounts(open []alertEntry) []metricSample {
	counts := map[string]uint64{alertQuiet: 0, alertNotMoving: 0, alertOffRoute: 0, alertNotStarted: 0}
	for _, a := range open {
		counts[a.Kind]++
	}
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	samples := make([]metricSample, 0, len(kinds))
	for _, kind := range kinds {
		samples = append(samples, metricSample{metricLabel("kind", kind), counts[kind]})
	}
	return samples
}

// buildInfoLabels names the Go version and, when the binary was built from a
// git checkout, the commit. The published image is built without .git, so
// there the revision is "unknown".
func buildInfoLabels() string {
	revision := "unknown"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return metricLabel("goversion", "unknown") + "," + metricLabel("revision", revision)
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			revision = s.Value
		}
	}
	return metricLabel("goversion", info.GoVersion) + "," + metricLabel("revision", revision)
}

// metricSample is one series of a family: its labels, written as
// metricLabel joins them, and its value.
type metricSample struct {
	labels string
	value  uint64
}

// writeMetric writes one family: its HELP and TYPE lines, then its samples.
func writeMetric(out *bytes.Buffer, name, kind, help string, samples ...metricSample) {
	fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
	for _, s := range samples {
		out.WriteString(name)
		if s.labels != "" {
			out.WriteString("{" + s.labels + "}")
		}
		out.WriteString(" " + strconv.FormatUint(s.value, 10) + "\n")
	}
}

// metricLabel writes name="value", escaping the value as the text format
// requires.
func metricLabel(name, value string) string {
	value = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(value)
	return name + `="` + value + `"`
}
