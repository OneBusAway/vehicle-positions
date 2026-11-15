package main

import (
	"fmt"
	"net/http"
)

// healthPageRefresh is how often, in seconds, the Feed health page reloads
// itself. It is sent as a Refresh header, so the page stays current without
// JavaScript. The counts are per minute, so reloading faster shows nothing new.
const healthPageRefresh = "60"

// healthStrip is the last hour's counts as the dashboard's feed health strip
// shows them.
type healthStrip struct {
	ReportsAccepted uint64
	ReportsRejected uint64
	// ErrorRate is the share of reports rejected, as "2.5%"; empty when no
	// report came in, since a rate of nothing is not 0%.
	ErrorRate string
	// TopReason is the most common reason for a rejection; empty when
	// nothing was rejected.
	TopReason   string
	FeedFetches uint64
	FeedRefused uint64
	// FeedFailed is fetches refused for a reason other than the key: a bad
	// request or a server error.
	FeedFailed uint64
}

func newHealthStrip(p healthPeriod) healthStrip {
	return healthStrip{
		ReportsAccepted: p.Reports.Accepted,
		ReportsRejected: p.Reports.Rejected(),
		ErrorRate:       errorRate(p.Reports),
		TopReason:       topReason(p.Reports),
		FeedFetches:     p.Feed.Total(),
		FeedRefused:     p.Feed.Refused,
		FeedFailed:      p.Feed.Invalid + p.Feed.ServerError,
	}
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
	return map[string]interface{}{
		"Title":    "Feed health",
		"Page":     "health",
		"Since":    formatTripTimestamp(snap.Since),
		"Strip":    newHealthStrip(snap.LastHour),
		"LastHour": snap.LastHour,
		"Total":    snap.Total,
		"Minutes":  rows,
	}
}

// healthPage renders the last hour of report and feed counts, per minute, and
// the totals since the server started.
func (ui *adminUI) healthPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Refresh", healthPageRefresh)
	ui.renderAdmin(w, r, http.StatusOK, "health.html", healthPageData(ui.health.snapshot()))
}
