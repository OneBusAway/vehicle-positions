package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
)

// serviceReportCSVHeader names the CSV's columns: the route rows of the JSON
// report, one per line, under the same names.
var serviceReportCSVHeader = []string{
	"date", "route_id", "short_name", "long_name", "trips_scheduled", "trips_run",
	"stops_on_time", "stops_early", "stops_late", "stops_unknown", "on_time_percent", "unknown_percent",
}

// handleServiceReport serves one service day's report: as JSON, or with
// format=csv its route rows as a CSV download.
func handleServiceReport(reports *serviceReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		format := q.Get("format")
		if format == "" {
			format = "json"
		}
		if format != "json" && format != "csv" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "format must be json or csv"})
			return
		}

		rep, err := reports.report(r.Context(), q.Get("date"))
		switch {
		case errors.Is(err, errBadReportDate):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		case errors.Is(err, errNoSchedule):
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		case err != nil:
			slog.Error("reports: work out a service report", "date", q.Get("date"), "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		if format == "csv" {
			writeServiceReportCSV(w, rep)
			return
		}
		writeJSON(w, http.StatusOK, rep)
	}
}

// writeServiceReportCSV writes the report's route rows. Route ids and names
// come from the agency's GTFS, text no one here checked, so they are escaped
// against formula injection like any other free text in a CSV export.
func writeServiceReportCSV(w http.ResponseWriter, rep *serviceReport) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="service-report-%s.csv"`, rep.Date))
	w.WriteHeader(http.StatusOK)

	writer := csv.NewWriter(w)
	if err := writer.Write(serviceReportCSVHeader); err != nil {
		slog.Error("reports: write CSV header", "date", rep.Date, "error", err)
		return
	}
	for _, route := range rep.Routes {
		record := []string{
			rep.Date,
			sanitizeCSVCell(route.RouteID),
			sanitizeCSVCell(route.ShortName),
			sanitizeCSVCell(route.LongName),
			strconv.Itoa(route.TripsScheduled),
			strconv.Itoa(route.TripsRun),
			strconv.Itoa(route.StopsOnTime),
			strconv.Itoa(route.StopsEarly),
			strconv.Itoa(route.StopsLate),
			strconv.Itoa(route.StopsUnknown),
			percentCell(route.OnTimePercent),
			percentCell(route.UnknownPercent),
		}
		if err := writer.Write(record); err != nil {
			slog.Error("reports: write CSV record", "date", rep.Date, "error", err)
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		slog.Error("reports: flush CSV response", "date", rep.Date, "error", err)
	}
}

// percentCell is a percentage to one decimal place, as the page shows it, or
// "" when there is none.
func percentCell(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', 1, 64)
}
