package main

import (
	"cmp"
	"math"
	"slices"
	"time"
)

// dayTotals is one service day as the Analytics page adds it up over a
// period: each route's counts by the hour its trips leave in, and when the
// trips that ran reached their last stop. A day older than yesterday is kept
// in the database in this form, so a period's past days are read rather than
// worked out again from every trail.
type dayTotals struct {
	// Date is the service date, YYYY-MM-DD.
	Date   string           `json:"-"`
	Routes []routeDayTotals `json:"routes"`
	// Arrivals counts the trips that ran by the whole minute, early (below
	// zero) or late, they reached their last stop in.
	Arrivals []arrivalMinute `json:"arrivals"`
	// ArrivalsLeftOut is the trips that ran but have no report at their last
	// stop.
	ArrivalsLeftOut int `json:"arrivals_left_out"`
}

// routeDayTotals is one route with service on the day, in the schedule's
// order, named as the day's report named it.
type routeDayTotals struct {
	RouteID   string       `json:"route_id"`
	ShortName string       `json:"short_name"`
	LongName  string       `json:"long_name"`
	Hours     []hourTotals `json:"hours"`
}

// hourTotals is a route's trips that leave in one hour of the service day,
// which can pass 23 for a trip after midnight.
type hourTotals struct {
	Hour      int `json:"hour"`
	Scheduled int `json:"scheduled"`
	Run       int `json:"run"`
	OnTime    int `json:"on_time"`
	Early     int `json:"early"`
	Late      int `json:"late"`
	Unknown   int `json:"unknown"`
}

// arrivalMinute is the trips that reached their last stop in one minute
// early or late, sorted by the on-time window the day was judged by.
type arrivalMinute struct {
	Minute int `json:"minute"`
	OnTime int `json:"on_time"`
	Early  int `json:"early"`
	Late   int `json:"late"`
}

func (h hourTotals) counts() reportCounts {
	return reportCounts{Scheduled: h.Scheduled, Run: h.Run, OnTime: h.OnTime, Early: h.Early, Late: h.Late, Unknown: h.Unknown}
}

func (r routeDayTotals) counts() reportCounts {
	var c reportCounts
	for _, h := range r.Hours {
		c.add(h.counts())
	}
	return c
}

func (d dayTotals) counts() reportCounts {
	var c reportCounts
	for _, r := range d.Routes {
		c.add(r.counts())
	}
	return c
}

// newDayTotals adds up rep, a day's service report worked out under s.
func newDayTotals(rep *serviceReport, s reportSettings) dayTotals {
	day := dayTotals{
		Date:     rep.Date,
		Routes:   make([]routeDayTotals, 0, len(rep.Routes)),
		Arrivals: make([]arrivalMinute, 0),
	}
	arrivals := make(map[int]*arrivalMinute)
	for _, route := range rep.Routes {
		hours := make(map[int]*hourTotals)
		for _, trip := range route.Trips {
			hour := serviceHour(rep, trip)
			h, ok := hours[hour]
			if !ok {
				h = &hourTotals{Hour: hour}
				hours[hour] = h
			}
			h.Scheduled++
			// Only a trip that ran has stops judged, so these add nothing for
			// the others.
			h.OnTime += trip.StopsOnTime
			h.Early += trip.StopsEarly
			h.Late += trip.StopsLate
			h.Unknown += trip.StopsUnknown
			if len(trip.Runs) == 0 {
				continue
			}
			h.Run++
			if trip.ArrivalDeviationS == nil {
				day.ArrivalsLeftOut++
				continue
			}
			d := time.Duration(*trip.ArrivalDeviationS) * time.Second
			minute := int(math.Floor(d.Minutes()))
			a, ok := arrivals[minute]
			if !ok {
				a = &arrivalMinute{Minute: minute}
				arrivals[minute] = a
			}
			switch s.verdict(d) {
			case stopEarly:
				a.Early++
			case stopLate:
				a.Late++
			default:
				a.OnTime++
			}
		}
		totals := routeDayTotals{RouteID: route.RouteID, ShortName: route.ShortName, LongName: route.LongName, Hours: make([]hourTotals, 0, len(hours))}
		for _, h := range hours {
			totals.Hours = append(totals.Hours, *h)
		}
		slices.SortFunc(totals.Hours, func(a, b hourTotals) int { return cmp.Compare(a.Hour, b.Hour) })
		day.Routes = append(day.Routes, totals)
	}
	for _, a := range arrivals {
		day.Arrivals = append(day.Arrivals, *a)
	}
	slices.SortFunc(day.Arrivals, func(a, b arrivalMinute) int { return cmp.Compare(a.Minute, b.Minute) })
	return day
}
