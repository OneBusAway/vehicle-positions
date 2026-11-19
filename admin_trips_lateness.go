package main

import (
	"context"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// tripsLateness is how far from the timetable each running trip of trips
// stood at the last stop it passed, by trip id. It is the figure the trip's
// own page heads with, worked out by that page's code, so the two never
// disagree. Only the trips given are read, in one read of their trails, so the
// Trips list works out the page it shows and no more.
//
// A trip has no entry, rather than an on-time one, when it has passed no stop
// it can be judged at yet, when ix does not have its GTFS trip or no schedule
// is loaded, and when it has ended: an ended trip's figure would need its
// trail read again, and is on its own page and in Analytics.
func (ui *adminUI) tripsLateness(ctx context.Context, trips []TripSummary, ix *rider.Index, now time.Time) (map[int64]*reportDeviation, error) {
	lateness := make(map[int64]*reportDeviation)
	if ix == nil {
		return lateness, nil
	}
	running := make([]TripSummary, 0, len(trips))
	ids := make([]int64, 0, len(trips))
	for _, t := range trips {
		if t.Status != "active" {
			continue
		}
		if _, ok := ix.Trip(t.GtfsTripID); !ok {
			continue
		}
		running = append(running, t)
		ids = append(ids, t.ID)
	}
	if len(ids) == 0 {
		return lateness, nil
	}
	trails, err := ui.reports.store.ListTrails(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, t := range running {
		if last := newTripDetail(t, trails[t.ID], ix, ui.reports.settings, ui.cfg.stalenessThreshold, now).LastStop; last != nil {
			lateness[t.ID] = last
		}
	}
	return lateness, nil
}
