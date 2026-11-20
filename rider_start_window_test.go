package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A rider who taps start at ST2, 25 minutes into T1's run, before the first
// run of a window that opens at midnight could have reached them is waiting
// for that run. Shifted back to its start, the tap lands before the window
// and in no window on the day before, so the ride is dated by the boarding
// time instead (#512). A shifted start that lands in a window still decides
// the date, and so does one when neither is in a window. T1 runs on weekdays; 2026-09-14 is a Monday.
func TestStartRide_EarlyTapBeforeAMidnightWindow(t *testing.T) {
	tests := []struct {
		name          string
		window        string
		day, h, m     int // when the rider taps start at ST2
		wantStartDate string
	}{
		{"monday, the day before not run", "T1,00:00:00,24:00:00,600,0", 14, 0, 5, "20260914"},
		{"tuesday, the day before's window over", "T1,00:00:00,02:00:00,600,0", 15, 0, 5, "20260915"},
		{"the shifted start in the day before's window", "T1,00:00:00,24:00:00,600,0", 15, 0, 10, "20260914"},
		{"neither in a window, a late run of the day before", "T1,06:00:00,23:30:00,600,0", 15, 0, 10, "20260914"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := headwayStartEnv(t, tc.window)
			_, tok := env.register(t)
			env.now = time.Date(2026, 9, tc.day, tc.h, tc.m, 0, 0, env.index.Timezone())

			ride := env.startRide(t, tok, map[string]any{"trip_id": "T1", "boarding_stop_id": "ST2"})
			require.Equal(t, tc.wantStartDate, env.store.rides[ride.RideID].StartDate)
		})
	}
}
