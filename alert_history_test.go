package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/OneBusAway/vehicle-positions/rider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyClear is one ClearAlert call.
type historyClear struct {
	id int64
	at time.Time
}

// historyLeftOpen is one ClearAlertsLeftOpen call.
type historyLeftOpen struct {
	keep []int64
	at   time.Time
}

// fakeAlertHistory is an AlertHistoryStore that records each call. Each raise
// gets the next id, and raiseErr and clearErr fail the calls while set. With
// clearCommits, a clear that fails is recorded all the same, as one whose
// reply was lost after it committed.
type fakeAlertHistory struct {
	mu           sync.Mutex
	nextID       int64
	raised       []alertEntry
	cleared      []historyClear
	leftOpen     []historyLeftOpen
	raiseErr     error
	clearErr     error
	clearCommits bool
}

func (f *fakeAlertHistory) RaiseAlert(_ context.Context, a alertEntry) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.raiseErr != nil {
		return 0, f.raiseErr
	}
	f.nextID++
	f.raised = append(f.raised, a)
	return f.nextID, nil
}

func (f *fakeAlertHistory) ClearAlert(_ context.Context, id int64, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.clearErr != nil && !f.clearCommits {
		return f.clearErr
	}
	f.cleared = append(f.cleared, historyClear{id, at})
	return f.clearErr
}

func (f *fakeAlertHistory) ClearAlertsLeftOpen(_ context.Context, keep []int64, at time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leftOpen = append(f.leftOpen, historyLeftOpen{keep, at})
	return 0, nil
}

func (f *fakeAlertHistory) failRaises(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raiseErr = err
}

func (f *fakeAlertHistory) failClears(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearErr = err
	f.clearCommits = false
}

func (f *fakeAlertHistory) failClearsAfterCommit(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearErr = err
	f.clearCommits = true
}

func (f *fakeAlertHistory) raisedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.raised)
}

// startRecorder starts a recorder that never checks by itself, so the test
// calls check, and stops it when the test ends. The watcher started at
// alertT0, and the rows a restart left open are settled ten minutes later.
func startRecorder(t *testing.T, store AlertHistoryStore, source alertSource) *alertRecorder {
	t.Helper()
	r := newAlertRecorder(store, source, alertT0, 10*time.Minute, time.Hour)
	t.Cleanup(r.Stop)
	return r
}

func TestAlertRecorder_RaisedThenCleared(t *testing.T) {
	store := &fakeAlertHistory{}
	source := &scriptedAlerts{}
	r := startRecorder(t, store, source)
	quiet := webhookAlert(alertQuiet)

	source.set(nil, quiet)
	r.check(alertT0.Add(6 * time.Minute))
	source.set(nil)
	r.check(alertT0.Add(7 * time.Minute))

	assert.Equal(t, []alertEntry{quiet}, store.raised, "raised once, as the watcher listed it")
	assert.Equal(t, []historyClear{{1, alertT0.Add(7 * time.Minute)}}, store.cleared, "its row cleared at the check it went")
}

func TestAlertRecorder_RaisedAgainAfterItClears(t *testing.T) {
	store := &fakeAlertHistory{}
	source := &scriptedAlerts{}
	r := startRecorder(t, store, source)

	for i, alerts := range [][]alertEntry{{webhookAlert(alertQuiet)}, nil, {webhookAlert(alertQuiet)}, nil} {
		source.set(nil, alerts...)
		r.check(alertT0.Add(time.Duration(6+i) * time.Minute))
	}

	assert.Len(t, store.raised, 2, "an alert that comes back is a new row")
	assert.Equal(t, []historyClear{{1, alertT0.Add(7 * time.Minute)}, {2, alertT0.Add(9 * time.Minute)}}, store.cleared)
}

func TestAlertRecorder_OneRowWhileTheAlertStays(t *testing.T) {
	store := &fakeAlertHistory{}
	source := &scriptedAlerts{}
	r := startRecorder(t, store, source)

	// A not-moving vehicle's since can shift while the alert stays.
	for i := range 3 {
		a := webhookAlert(alertNotMoving)
		a.Since = a.Since.Add(time.Duration(i) * time.Minute)
		source.set(nil, a)
		r.check(alertT0.Add(time.Duration(6+i) * time.Minute))
	}

	assert.Len(t, store.raised, 1, "the same alert across checks is one row")
	assert.Empty(t, store.cleared)
}

func TestAlertRecorder_ChangeOfKindIsTwoAlerts(t *testing.T) {
	store := &fakeAlertHistory{}
	source := &scriptedAlerts{}
	r := startRecorder(t, store, source)

	source.set(nil, webhookAlert(alertOffRoute))
	r.check(alertT0.Add(6 * time.Minute))
	source.set(nil, webhookAlert(alertNotMoving))
	r.check(alertT0.Add(7 * time.Minute))

	require.Len(t, store.raised, 2)
	assert.Equal(t, alertNotMoving, store.raised[1].Kind)
	assert.Equal(t, []historyClear{{1, alertT0.Add(7 * time.Minute)}}, store.cleared)
}

func TestAlertRecorder_FailedCheckChangesNothing(t *testing.T) {
	store := &fakeAlertHistory{}
	source := &scriptedAlerts{}
	r := startRecorder(t, store, source)

	source.set(nil, webhookAlert(alertQuiet))
	r.check(alertT0.Add(6 * time.Minute))
	source.set(errors.New("database down"))
	r.check(alertT0.Add(7 * time.Minute))
	source.set(nil, webhookAlert(alertQuiet))
	r.check(alertT0.Add(8 * time.Minute))

	assert.Len(t, store.raised, 1, "an outage neither clears an alert nor raises it again")
	assert.Empty(t, store.cleared)
}

func TestAlertRecorder_FailedWritesAreTriedAgain(t *testing.T) {
	captureWebhookLogs(t)
	store := &fakeAlertHistory{}
	source := &scriptedAlerts{}
	r := startRecorder(t, store, source)

	store.failRaises(errors.New("database down"))
	source.set(nil, webhookAlert(alertQuiet))
	r.check(alertT0.Add(6 * time.Minute))
	store.failRaises(nil)
	r.check(alertT0.Add(7 * time.Minute))
	require.Len(t, store.raised, 1, "a raise that failed is made at the next check")

	store.failClears(errors.New("database down"))
	source.set(nil)
	r.check(alertT0.Add(8 * time.Minute))
	store.failClears(nil)
	r.check(alertT0.Add(9 * time.Minute))
	assert.Equal(t, []historyClear{{1, alertT0.Add(9 * time.Minute)}}, store.cleared, "a clear that failed is made at the next check")
}

func TestAlertRecorder_RaisedAgainAfterAClearThatFailed(t *testing.T) {
	captureWebhookLogs(t)
	store := &fakeAlertHistory{}
	source := &scriptedAlerts{}
	r := startRecorder(t, store, source)

	source.set(nil, webhookAlert(alertQuiet))
	r.check(alertT0.Add(6 * time.Minute))
	store.failClearsAfterCommit(errors.New("connection reset"))
	source.set(nil)
	r.check(alertT0.Add(7 * time.Minute))
	store.failClears(nil)
	source.set(nil, webhookAlert(alertQuiet))
	r.check(alertT0.Add(8 * time.Minute))
	r.check(alertT0.Add(9 * time.Minute))
	source.set(nil)
	r.check(alertT0.Add(10 * time.Minute))

	assert.Len(t, store.raised, 2, "the clear may have gone through, so the alert that comes back is raised again, once")
	assert.Equal(t, []historyClear{{1, alertT0.Add(7 * time.Minute)}, {2, alertT0.Add(10 * time.Minute)}}, store.cleared,
		"and it clears by the row that raise gave")
}

func TestAlertRecorder_LeftOpenClearedOnceSettled(t *testing.T) {
	store := &fakeAlertHistory{}
	source := &scriptedAlerts{}
	r := startRecorder(t, store, source)
	source.set(nil, webhookAlert(alertQuiet))

	r.check(alertT0.Add(9 * time.Minute))
	assert.Empty(t, store.leftOpen, "before settle, an alert left open may still come back")

	r.check(alertT0.Add(10 * time.Minute))
	r.check(alertT0.Add(11 * time.Minute))
	assert.Equal(t, []historyLeftOpen{{[]int64{1}, alertT0}}, store.leftOpen,
		"once settled, every row but the alerts open now is cleared at the start, once")
}

func TestAlertRecorder_LeftOpenWithNoneOpenClearsThemAll(t *testing.T) {
	store := &fakeAlertHistory{}
	r := startRecorder(t, store, &scriptedAlerts{})

	r.check(alertT0.Add(10 * time.Minute))

	require.Len(t, store.leftOpen, 1)
	assert.NotNil(t, store.leftOpen[0].keep, "a nil keep reaches Postgres as NULL, which keeps every row")
	assert.Empty(t, store.leftOpen[0].keep)
}

func TestAlertRecorder_LeftOpenWaitsForTheRaises(t *testing.T) {
	captureWebhookLogs(t)
	store := &fakeAlertHistory{}
	source := &scriptedAlerts{}
	r := startRecorder(t, store, source)

	store.failRaises(errors.New("database down"))
	source.set(nil, webhookAlert(alertQuiet))
	r.check(alertT0.Add(10 * time.Minute))
	assert.Empty(t, store.leftOpen, "a raise that failed may have opened its row, which must not be cleared")

	store.failRaises(nil)
	r.check(alertT0.Add(11 * time.Minute))
	assert.Equal(t, []historyLeftOpen{{[]int64{1}, alertT0}}, store.leftOpen)
}

func TestAlertHistorySettle(t *testing.T) {
	assert.Equal(t, 11*time.Minute, alertHistorySettle(offRouteSettings), "ALERT_STOPPED_AFTER's 10 minutes and one more")

	settings := offRouteSettings
	settings.offRouteAfter = 20 * time.Minute
	assert.Equal(t, 21*time.Minute, alertHistorySettle(settings))
	settings.quietAfter = 30 * time.Minute
	assert.Equal(t, 31*time.Minute, alertHistorySettle(settings))
}

// lockedTripLister is a TripLister the recorder and the webhook can both read
// at once.
type lockedTripLister struct {
	mu    sync.Mutex
	trips []TripSummary
}

func (l *lockedTripLister) ListTrips(context.Context, TripFilter) ([]TripSummary, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.trips, nil
}

// quietWatcher is a watcher with one vehicle quiet since its trip started an
// hour ago.
func quietWatcher() *alertWatcher {
	start := time.Now().Add(-time.Hour)
	w := newAlertWatcher(&lockedTripLister{trips: []TripSummary{alertTrip("bus-7", start)}}, offRouteSettings)
	w.watchingSince = start
	return w
}

func TestStartAlertJobs(t *testing.T) {
	ix := func() *rider.Index { return nil }

	t.Run("without a webhook URL the alerts are still recorded", func(t *testing.T) {
		store := &fakeAlertHistory{}
		stop := startAlertJobs(store, quietWatcher(), ix, nil, testWebhookTiming())
		t.Cleanup(stop)

		require.Eventually(t, func() bool { return store.raisedCount() > 0 }, 5*time.Second, time.Millisecond)
	})

	t.Run("with one the webhook posts as well", func(t *testing.T) {
		receiver := newWebhookReceiver(t, http.StatusOK)
		store := &fakeAlertHistory{}
		cfg := configFor(receiver)
		stop := startAlertJobs(store, quietWatcher(), ix, &cfg, testWebhookTiming())
		t.Cleanup(stop)

		receiver.waitFor(t, 1)
		require.Eventually(t, func() bool { return store.raisedCount() > 0 }, 5*time.Second, time.Millisecond)
		post := receiver.payloads(t)[0]
		assert.Equal(t, "raised", post["event"])
		assert.Equal(t, "quiet", post["kind"])
		assert.Equal(t, "bus-7", post["vehicle_id"])
	})
}
