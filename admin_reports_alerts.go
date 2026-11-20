package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// analyticsAlertRoutes is how many routes the Alerts section lists.
const analyticsAlertRoutes = 5

// analyticsAlerts is the Alerts section of the Analytics page: the alerts
// raised over the days the page shows.
type analyticsAlerts struct {
	Raised  int
	Cleared int
	Open    int
	// MedianClear and LongestClear are how long the cleared alerts took to
	// clear; "" when none has.
	MedianClear  string
	LongestClear string
	Kinds        []analyticsAlertKind
	Routes       []analyticsAlertRoute
	// Days is a row a day for the table behind the charts; nil for one day.
	Days []analyticsAlertDay
}

// analyticsAlertKind is one kind's alerts over the period, with a bar a day;
// Chart is nil for one day.
type analyticsAlertKind struct {
	Title  string
	URL    string
	Raised int
	Chart  *barChartView
}

// analyticsAlertRoute is a route with alerts raised on it, and Kinds the
// count of each kind in words.
type analyticsAlertRoute struct {
	Badge  tripRoute
	Name   string
	Raised int
	Kinds  string
}

// analyticsAlertDay is a day's alerts, Counts in the order of alertKinds.
type analyticsAlertDay struct {
	Day    string
	Counts []int
	Raised int
}

// newAnalyticsAlerts counts alerts over days, the service dates the page
// shows, oldest first. An alert counts on the service date ix puts its
// raising on, and the routes are badged from ix.
func newAnalyticsAlerts(alerts []raisedAlert, ix *rider.Index, days []time.Time) analyticsAlerts {
	out := analyticsAlerts{
		Kinds:  make([]analyticsAlertKind, 0, len(alertKinds)),
		Routes: make([]analyticsAlertRoute, 0),
	}
	dayAt := make(map[string]int, len(days))
	perDay := make([][]int, len(days))
	for i, day := range days {
		dayAt[day.Format(gtfsDateLayout)] = i
		perDay[i] = make([]int, len(alertKinds))
	}
	kindAt := make(map[string]int, len(alertKinds))
	for k, kind := range alertKinds {
		kindAt[kind.kind] = k
	}
	byKind := make([]int, len(alertKinds))
	byRoute := make(map[string][]int)
	var lasted []time.Duration
	for _, a := range alerts {
		k, ok := kindAt[a.Kind]
		if !ok {
			continue
		}
		out.Raised++
		byKind[k]++
		if i, ok := dayAt[ix.ServiceDate(a.RaisedAt)]; ok {
			perDay[i][k]++
		}
		if byRoute[a.RouteID] == nil {
			byRoute[a.RouteID] = make([]int, len(alertKinds))
		}
		byRoute[a.RouteID][k]++
		if a.ClearedAt == nil {
			out.Open++
			continue
		}
		lasted = append(lasted, a.ClearedAt.Sub(a.RaisedAt))
	}

	out.Cleared = len(lasted)
	if len(lasted) > 0 {
		slices.Sort(lasted)
		mid := len(lasted) / 2
		median := lasted[mid]
		if len(lasted)%2 == 0 {
			median = (lasted[mid-1] + lasted[mid]) / 2
		}
		out.MedianClear = formatClearTime(median)
		out.LongestClear = formatClearTime(lasted[len(lasted)-1])
	}

	// A day is one bar a kind, which says nothing its count does not.
	charted := len(days) > 1
	for k, kind := range alertKinds {
		row := analyticsAlertKind{Title: kind.title, URL: clearedAlertsURL(kind.kind, 1), Raised: byKind[k]}
		if charted {
			bars := make([]chartBar, 0, len(days))
			for i, day := range days {
				bars = append(bars, chartBar{Label: day.Format("2 Jan"), Value: uint64(perDay[i][k])})
			}
			chart := newBarChart(bars, barChartOptions{Name: kind.title + " raised by day", Height: 72, Unit: "raised", Peak: "Most in a day"})
			row.Chart = &chart
		}
		out.Kinds = append(out.Kinds, row)
	}
	if charted {
		out.Days = make([]analyticsAlertDay, 0, len(days))
		for i, day := range days {
			total := 0
			for _, n := range perDay[i] {
				total += n
			}
			out.Days = append(out.Days, analyticsAlertDay{Day: day.Format("Mon 2 Jan"), Counts: perDay[i], Raised: total})
		}
	}

	for routeID, counts := range byRoute {
		row := analyticsAlertRoute{Name: "No route"}
		if routeID != "" {
			row.Badge = routeBadge(ix, routeID)
			row.Name = routeTitle(routeID, row.Badge.Name, row.Badge.LongName)
		}
		kinds := make([]string, 0, len(counts))
		for k, n := range counts {
			if n > 0 {
				kinds = append(kinds, strconv.Itoa(n)+" "+strings.ToLower(alertKinds[k].title))
			}
			row.Raised += n
		}
		row.Kinds = strings.Join(kinds, ", ")
		row.Badge.ID = routeID
		out.Routes = append(out.Routes, row)
	}
	slices.SortFunc(out.Routes, func(a, b analyticsAlertRoute) int {
		return cmp.Or(cmp.Compare(b.Raised, a.Raised), cmp.Compare(a.Badge.ID, b.Badge.ID))
	})
	if len(out.Routes) > analyticsAlertRoutes {
		out.Routes = out.Routes[:analyticsAlertRoutes]
	}
	return out
}

// formatClearTime is how long an alert took to clear, to the minute.
func formatClearTime(d time.Duration) string {
	if d < time.Minute {
		return "under 1 min"
	}
	return formatMinutes(d)
}

// addAnalyticsAlerts adds the Alerts section to data, the Analytics page's,
// for the days query shows: query.Days of them to query.Date, the day the
// page settled on. A route's day lists its trips and has no section. Only
// the alerts raised in those days are read, from 03:00 on the first, when
// ServiceDate turns to it, to 03:00 after the last.
func addAnalyticsAlerts(ctx context.Context, store AlertsRaisedLister, ix *rider.Index, query analyticsQuery, data map[string]interface{}) error {
	if query.RouteID != "" || ix == nil {
		return nil
	}
	last, err := time.Parse(reportDateLayout, query.Date)
	if err != nil {
		return fmt.Errorf("alerts raised: %w", err)
	}
	days := make([]time.Time, query.Days)
	for i := range days {
		days[i] = last.AddDate(0, 0, i+1-query.Days)
	}
	from, err := ix.ServiceDateBegins(days[0].Format(gtfsDateLayout))
	if err != nil {
		return err
	}
	to, err := ix.ServiceDateBegins(last.AddDate(0, 0, 1).Format(gtfsDateLayout))
	if err != nil {
		return err
	}
	alerts, err := store.ListAlertsRaisedBetween(ctx, from, to)
	if err != nil {
		return err
	}
	data["AlertsRaised"] = newAnalyticsAlerts(alerts, ix, days)
	return nil
}
