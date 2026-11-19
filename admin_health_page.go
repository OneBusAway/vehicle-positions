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

// healthPageData is what the Feed health page renders: the hour newest first,
// since that is the minute an operator is looking for.
func healthPageData(snap feedHealthResponse) map[string]interface{} {
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
	for _, m := range snap.Minutes {
		fetches = append(fetches, chartBar{Label: m.Minute.UTC().Format("15:04"), Value: m.Feed.Served})
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
		"Reports":  reportsPerMinuteChart(snap, time.UTC, healthChartHeight),
		"Fetches": newBarChart(fetches, barChartOptions{
			Name:   "Feed fetches served a minute over the last hour",
			Height: healthChartHeight,
			Unit:   "served",
			Peak:   "Most in a minute",
		}),
	}
}

// healthPage renders the last hour of report and feed counts, per minute, and
// the totals since the server started.
func (ui *adminUI) healthPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Refresh", healthPageRefresh)
	ui.renderAdmin(w, r, http.StatusOK, "health.html", healthPageData(ui.health.snapshot()))
}
