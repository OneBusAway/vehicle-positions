package main

import (
	"fmt"
	"net/http"
	"time"
)

// healthPageRefresh is how often, in seconds, the Feed health page reloads
// itself. It is sent as a Refresh header, so the page stays current without
// JavaScript. The counts are per minute, so reloading faster shows nothing new.
const healthPageRefresh = "60"

// healthChartHeight is the height the page's two bar charts are drawn at.
const healthChartHeight = 130

// healthSummary is the last hour as the Feed health page's tiles show it.
type healthSummary struct {
	// Vehicles is how many vehicles reported in the last full minute, the
	// one before the minute in progress, which is still filling up.
	Vehicles int
	// VehiclesNote says which minute that was; "None in the last hour" when
	// no vehicle reported in any of them.
	VehiclesNote    string
	ReportsAccepted uint64
	// PerMinute is the accepted reports as a rate over the hour.
	PerMinute       string
	ReportsRejected uint64
	// ErrorRate is the share of reports rejected, as "2.5%"; empty when no
	// report came in, since a rate of nothing is not 0%.
	ErrorRate string
	// TopReason is the most common reason for a rejection; empty when
	// nothing was rejected.
	TopReason  string
	FeedServed uint64
	// LastFetch is the minute the newest fetch was served in, on the UTC
	// clock the page's minutes are on; empty when none was in the hour.
	LastFetch   string
	FeedRefused uint64
}

func newHealthSummary(snap feedHealthResponse) healthSummary {
	p := snap.LastHour
	s := healthSummary{
		ReportsAccepted: p.Reports.Accepted,
		PerMinute:       "None in the last hour",
		ReportsRejected: p.Reports.Rejected(),
		ErrorRate:       errorRate(p.Reports),
		TopReason:       topReason(p.Reports),
		FeedServed:      p.Feed.Served,
		FeedRefused:     p.Feed.Refused,
	}
	switch perMinute := (p.Reports.Accepted + healthWindowMinutes/2) / healthWindowMinutes; {
	case perMinute > 0:
		s.PerMinute = fmt.Sprintf("About %d a minute", perMinute)
	case p.Reports.Accepted > 0:
		s.PerMinute = "Under 1 a minute"
	}
	for _, m := range snap.Minutes {
		if m.Feed.Served > 0 {
			s.LastFetch = m.Minute.UTC().Format("15:04")
		}
		if m.Vehicles > 0 {
			s.VehiclesNote = "Last full minute, at "
		}
	}
	if n := len(snap.Minutes); s.VehiclesNote == "" || n < 2 {
		s.VehiclesNote = "None in the last hour"
	} else {
		last := snap.Minutes[n-2]
		s.Vehicles = last.Vehicles
		s.VehiclesNote += last.Minute.UTC().Format("15:04") + " UTC"
	}
	return s
}

// healthHeadline is the page's one sentence. Reports the server turned away
// are a fact about the phones, not an alarm, until they outnumber the ones it
// took; a request the server failed is the server's own fault.
func healthHeadline(p healthPeriod) string {
	failed := p.Reports.ServerError + p.Feed.ServerError
	switch {
	case failed == 1:
		return "The server failed 1 request in the last hour"
	case failed > 0:
		return fmt.Sprintf("The server failed %d requests in the last hour", failed)
	case p.Reports.Rejected() > p.Reports.Accepted:
		return "Most reports were rejected in the last hour"
	case p.Reports.Total()+p.Feed.Total() == 0:
		return "Nothing has come in during the last hour"
	}
	return "Reports and the feed are healthy"
}

func errorRate(r reportOutcomes) string {
	total := r.Total()
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("%.1f%%", float64(r.Rejected())*100/float64(total))
}

// topReason names the most common rejection. A tie goes to the reason listed
// first, in the order the Feed health page lists them.
func topReason(r reportOutcomes) string {
	reasons := []struct {
		label string
		count uint64
	}{
		{"invalid", r.Invalid},
		{"signed out or not allowed", r.Unauthorized},
		{"rate limited", r.RateLimited},
		{"server error", r.ServerError},
	}
	best, bestCount := "", uint64(0)
	for _, reason := range reasons {
		if reason.count > bestCount {
			best, bestCount = reason.label, reason.count
		}
	}
	return best
}

// healthMinuteRow is one row of the Feed health page's per-minute table.
type healthMinuteRow struct {
	Minute  string
	Reports reportOutcomes
	Feed    feedOutcomes
}

// healthHourRow is one row of the Feed health page's per-hour table.
type healthHourRow struct {
	Hour     string
	Vehicles int
	Reports  reportOutcomes
	Feed     feedOutcomes
}

// healthRangeLink is one of the hours section's two periods.
type healthRangeLink struct {
	Label   string
	URL     string
	Current bool
}

// healthHoursSection is the hours section of the Feed health page: the last
// day or week, from the hours saved and the one in progress.
type healthHoursSection struct {
	// Period is "24 hours" or "7 days", as the headings and charts name it.
	Period string
	Range  []healthRangeLink
	// Error is set, and the rest empty, when the saved hours could not be
	// read.
	Error    string
	Total    healthPeriod
	Reports  barChartView
	Vehicles barChartView
	Fetches  barChartView
	// Rows are newest first, as the minutes are.
	Rows []healthHourRow
}

func newHealthHoursSection(snap feedHealthResponse, days int) healthHoursSection {
	section := healthHoursSection{
		Period: "24 hours",
		Range: []healthRangeLink{
			{Label: "24 hours", URL: "/admin/health", Current: days == 1},
			{Label: "7 days", URL: "/admin/health?days=7", Current: days == 7},
		},
		Error: snap.HoursError,
	}
	// An hour of the week is named with its day, since 24 of them share each
	// time of day.
	layout := "15:04"
	if days == 7 {
		section.Period, layout = "7 days", "Jan 2 15:04"
	}
	if section.Error != "" {
		return section
	}
	reports := make([]chartBar, 0, len(snap.Hours))
	vehicles := make([]chartBar, 0, len(snap.Hours))
	fetches := make([]chartBar, 0, len(snap.Hours))
	var total healthCounts
	for _, h := range snap.Hours {
		label := h.Hour.UTC().Format(layout)
		reports = append(reports, chartBar{Label: label, Value: h.Reports.Accepted, Warn: h.Reports.Rejected()})
		vehicles = append(vehicles, chartBar{Label: label, Value: uint64(h.Vehicles)})
		fetches = append(fetches, chartBar{Label: label, Value: h.Feed.Served})
		total.add(countsOf(h))
	}
	section.Total = newHealthPeriod(total)
	section.Rows = make([]healthHourRow, 0, len(snap.Hours))
	for i := len(snap.Hours) - 1; i >= 0; i-- {
		h := snap.Hours[i]
		section.Rows = append(section.Rows, healthHourRow{Hour: h.Hour.UTC().Format(layout), Vehicles: h.Vehicles, Reports: h.Reports, Feed: h.Feed})
	}
	over := " an hour over the last " + section.Period
	section.Reports = newBarChart(reports, barChartOptions{
		Name: "Location reports" + over, Height: healthChartHeight, Unit: "accepted", WarnUnit: "rejected", Peak: "Most in an hour",
	})
	section.Vehicles = newBarChart(vehicles, barChartOptions{
		Name: "Vehicles reporting" + over, Height: healthChartHeight, Unit: "reporting", Peak: "Most in an hour",
	})
	section.Fetches = newBarChart(fetches, barChartOptions{
		Name: "Feed fetches served" + over, Height: healthChartHeight, Unit: "served", Peak: "Most in an hour",
	})
	return section
}

// healthPageData is what the Feed health page renders: the hour newest first,
// since that is the minute an operator is looking for, and the last days'
// hours.
func healthPageData(snap feedHealthResponse, days int) map[string]interface{} {
	rows := make([]healthMinuteRow, 0, len(snap.Minutes))
	for i := len(snap.Minutes) - 1; i >= 0; i-- {
		m := snap.Minutes[i]
		rows = append(rows, healthMinuteRow{
			Minute:  m.Minute.UTC().Format("15:04"),
			Reports: m.Reports,
			Feed:    m.Feed,
		})
	}
	fetches := make([]chartBar, 0, len(snap.Minutes))
	vehicles := make([]chartBar, 0, len(snap.Minutes))
	for _, m := range snap.Minutes {
		fetches = append(fetches, chartBar{Label: m.Minute.UTC().Format("15:04"), Value: m.Feed.Served})
		vehicles = append(vehicles, chartBar{Label: m.Minute.UTC().Format("15:04"), Value: uint64(m.Vehicles)})
	}
	return map[string]interface{}{
		"Title":    "Feed health",
		"Page":     "health",
		"Headline": healthHeadline(snap.LastHour),
		"Since":    formatTripTimestamp(snap.Since),
		"Summary":  newHealthSummary(snap),
		"LastHour": snap.LastHour,
		"Total":    snap.Total,
		"Minutes":  rows,
		"Hours":    newHealthHoursSection(snap, days),
		"Reports":  reportsPerMinuteChart(snap, time.UTC, healthChartHeight),
		"Vehicles": newBarChart(vehicles, barChartOptions{
			Name:   "Vehicles reporting a minute over the last hour",
			Height: healthChartHeight,
			Unit:   "reporting",
			Peak:   "Most in a minute",
		}),
		"Fetches": newBarChart(fetches, barChartOptions{
			Name:   "Feed fetches served a minute over the last hour",
			Height: healthChartHeight,
			Unit:   "served",
			Peak:   "Most in a minute",
		}),
	}
}

// healthPage renders the last hour of report and feed counts, per minute, the
// last day's or week's per hour, and the totals since the server started. Any
// ?days but 7 is the day, as the Analytics page opens on its default for a
// period it does not know.
func (ui *adminUI) healthPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Refresh", healthPageRefresh)
	days, ok := parseHealthDays(r)
	if !ok {
		days = 1
	}
	ui.renderAdmin(w, r, http.StatusOK, "health.html", healthPageData(ui.health.report(r.Context(), days), days))
}
