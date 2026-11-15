package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAlerts_NotMovingCountsOnlyReportedTime: not_moving is for a vehicle that
// is still reporting and has stayed within 50 m for ALERT_STOPPED_AFTER. The
// silence after its latest report, short of ALERT_QUIET_AFTER, is not time
// it was seen standing still.
func TestAlerts_NotMovingCountsOnlyReportedTime(t *testing.T) {
	firstReport := alertT0.Add(10 * time.Second)

	t.Run("reports stop short of the setting", func(t *testing.T) {
		w := newTestWatcher()
		lastReport := firstReport.Add(8 * time.Minute)
		reportEvery(w, "bus-7", alertStop, firstReport, lastReport, 5*time.Second)

		// Ten minutes after the first report and two after the last: under
		// ALERT_QUIET_AFTER, so not quiet either.
		got := w.alerts([]TripSummary{alertTrip("bus-7", alertT0)}, firstReport.Add(testAlertSettings.stoppedAfter))

		assert.Empty(t, got, "eight minutes of reports from one place are not ten")
	})

	t.Run("one report with a setting under the quiet one", func(t *testing.T) {
		w := newAlertWatcher(&noopStore{}, alertSettings{quietAfter: 3 * time.Minute, stoppedAfter: time.Minute})
		w.watchingSince = alertT0.Add(-time.Hour)
		w.record("bus-7", alertStop, firstReport)

		got := w.alerts([]TripSummary{alertTrip("bus-7", alertT0)}, firstReport.Add(2*time.Minute))

		assert.Empty(t, got, "a single report says nothing about how long the vehicle stood there")
	})

	t.Run("the reports reach the setting", func(t *testing.T) {
		w := newTestWatcher()
		lastReport := firstReport.Add(testAlertSettings.stoppedAfter)
		reportEvery(w, "bus-7", alertStop, firstReport, lastReport, 5*time.Second)
		now := lastReport.Add(2 * time.Minute)

		got := w.alerts([]TripSummary{alertTrip("bus-7", alertT0)}, now)

		require.Len(t, got, 1)
		assert.Equal(t, alertNotMoving, got[0].Kind)
		assert.Equal(t, firstReport, got[0].Since)
		assert.Equal(t, int64(now.Sub(firstReport)/time.Second), got[0].DurationSeconds)
	})
}
