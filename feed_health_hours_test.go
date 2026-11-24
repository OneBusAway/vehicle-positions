package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHourStore keeps saved hours in memory, keyed by the hour's start, as
// feed_health_hours does: a save replaces the hour's row. saveErr and listErr,
// when set, fail every call of their kind.
type fakeHourStore struct {
	mu      sync.Mutex
	rows    map[time.Time]healthHour
	saves   []healthHour
	saveErr error
	listErr error
}

func newFakeHourStore() *fakeHourStore {
	return &fakeHourStore{rows: make(map[time.Time]healthHour)}
}

func (f *fakeHourStore) ListFeedHealthHours(_ context.Context, first, last time.Time) ([]healthHour, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]healthHour, 0)
	for at := first; !at.After(last); at = at.Add(time.Hour) {
		if row, ok := f.rows[at]; ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *fakeHourStore) SaveFeedHealthHour(_ context.Context, hour healthHour) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves = append(f.saves, hour)
	f.rows[hour.Hour] = hour
	return nil
}

func (f *fakeHourStore) saved() []healthHour {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]healthHour(nil), f.saves...)
}

func (f *fakeHourStore) failSaves(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveErr = err
}

// healthTestHour is the hour healthTestStart is in.
var healthTestHour = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

// newStoredFeedHealth is a feedHealth on clock that keeps its hours in store.
func newStoredFeedHealth(t *testing.T, clock *healthTestClock, store healthHourStore) *feedHealth {
	t.Helper()
	h := newFeedHealth(clock.now)
	require.NoError(t, h.restoreHours(context.Background(), store))
	return h
}

func TestFeedHealthHours_AnHourIsSavedOnceWhenItCloses(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	h := newStoredFeedHealth(t, clock, store)
	ctx := context.Background()

	h.record(healthReports, http.StatusCreated)
	h.record(healthReports, http.StatusBadRequest)
	h.record(healthFeed, http.StatusOK)
	h.record(healthFeed, http.StatusUnauthorized)
	sendReport(t, h, liveReportPath, "bus-1", http.StatusCreated)
	h.saveClosedHours(ctx)
	assert.Empty(t, store.saved(), "the hour in progress is not saved")

	// Into the next hour, with a report there that must not reach 09:00.
	clock.advance(50 * time.Minute)
	h.record(healthReports, http.StatusCreated)
	h.saveClosedHours(ctx)
	h.saveClosedHours(ctx)

	assert.Equal(t, []healthHour{{
		Hour:     healthTestHour,
		Reports:  reportOutcomes{Accepted: 2, Invalid: 1},
		Feed:     feedOutcomes{Served: 1, Refused: 1},
		Vehicles: 1,
	}}, store.saved(), "saved once, at its close")
}

func TestFeedHealthHours_AnHourWithNothingInItIsSavedToo(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	h := newStoredFeedHealth(t, clock, store)

	clock.advance(time.Hour)
	h.saveClosedHours(context.Background())

	assert.Equal(t, []healthHour{{Hour: healthTestHour}}, store.saved(),
		"a quiet hour is closed by the clock, not by the next request")
}

func TestFeedHealthHours_AFailedSaveIsTriedAgain(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	h := newStoredFeedHealth(t, clock, store)
	ctx := context.Background()

	h.record(healthReports, http.StatusCreated)
	clock.advance(time.Hour)
	store.failSaves(errors.New("connection refused"))
	h.saveClosedHours(ctx)
	assert.Empty(t, store.saved())

	store.failSaves(nil)
	h.saveClosedHours(ctx)
	assert.Equal(t, []healthHour{{Hour: healthTestHour, Reports: reportOutcomes{Accepted: 1}}}, store.saved())
}

func TestFeedHealthHours_HoursWaitingToBeSavedAreBounded(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	h := newFeedHealth(clock.now)

	// No store: nothing saves them, so they pile up as far as the cap.
	for range healthWeekHours + 5 {
		h.record(healthReports, http.StatusCreated)
		clock.advance(time.Hour)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	require.Len(t, h.closed, healthWeekHours)
	// Each record closed the hour before it: 172 closed, the 4 oldest dropped.
	assert.Equal(t, unixHour(healthTestStart)+4, h.closed[0].hour, "the oldest go first")
}

func TestFeedHealthHours_HoursKeptByAFailedSaveAreBounded(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	store.failSaves(errors.New("connection refused"))
	h := newStoredFeedHealth(t, clock, store)
	ctx := context.Background()

	// The save fails each time, with every hour kept, for longer than the cap
	// holds.
	for range healthWeekHours + 30 {
		h.record(healthReports, http.StatusCreated)
		clock.advance(time.Hour)
		h.saveClosedHours(ctx)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	assert.LessOrEqual(t, len(h.closed), healthWeekHours)
	assert.Equal(t, unixHour(healthTestStart)+healthWeekHours+30-1, h.closed[len(h.closed)-1].hour, "the newest are kept")
}

func TestFeedHealthHours_AClockSteppedBackCountsInTheHourInProgress(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	h := newStoredFeedHealth(t, clock, store)
	ctx := context.Background()

	h.record(healthReports, http.StatusCreated)
	clock.advance(-time.Hour)
	h.record(healthReports, http.StatusCreated)
	h.saveClosedHours(ctx)
	assert.Empty(t, store.saved(), "08:00 is not opened again, so 09:00 is not closed")

	clock.advance(2 * time.Hour)
	h.saveClosedHours(ctx)
	assert.Equal(t, []healthHour{{Hour: healthTestHour, Reports: reportOutcomes{Accepted: 2}}}, store.saved())
}

func TestFeedHealthHours_AVehicleCountsOnceAnHour(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	h := newStoredFeedHealth(t, clock, store)

	sendReport(t, h, liveReportPath, "bus-1", http.StatusCreated)
	clock.advance(time.Minute)
	sendReport(t, h, liveReportPath, "bus-1", http.StatusCreated)
	sendReport(t, h, liveReportPath, "bus-2", http.StatusCreated)
	clock.advance(time.Minute)
	sendReport(t, h, liveReportPath, "bus-1", http.StatusCreated)
	sendReport(t, h, "/api/v1/locations/history", "bus-3", http.StatusCreated)
	sendReport(t, h, liveReportPath, "bus-4", http.StatusBadRequest)
	// 10:00 to 10:01: bus-1 again, in an hour of its own.
	clock.advance(45 * time.Minute)
	sendReport(t, h, liveReportPath, "bus-1", http.StatusCreated)
	clock.advance(time.Hour)
	h.saveClosedHours(context.Background())

	saved := store.saved()
	require.Len(t, saved, 2)
	assert.Equal(t, 2, saved[0].Vehicles, "bus-1 and bus-2; history and refused reports do not count")
	assert.Equal(t, 1, saved[1].Vehicles)
}

func TestFeedHealthHours_StopSavesTheHourInProgress(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	h := newFeedHealth(clock.now)
	writer := startHealthHours(context.Background(), h, store, time.Hour)

	h.record(healthReports, http.StatusCreated)
	clock.advance(time.Hour)
	h.record(healthFeed, http.StatusOK)
	writer.Stop()
	writer.Stop()

	assert.Equal(t, []healthHour{
		{Hour: healthTestHour, Reports: reportOutcomes{Accepted: 1}},
		{Hour: healthTestHour.Add(time.Hour), Feed: feedOutcomes{Served: 1}},
	}, store.saved(), "the closed hour and the one in progress, each once")
}

func TestFeedHealthHours_WriterSavesOnItsTick(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	h := newFeedHealth(clock.now)
	writer := startHealthHours(context.Background(), h, store, time.Millisecond)
	t.Cleanup(writer.Stop)

	h.record(healthReports, http.StatusCreated)
	clock.advance(time.Hour)

	assert.Eventually(t, func() bool { return len(store.saved()) == 1 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, healthTestHour, store.saved()[0].Hour)
}

// TestFeedHealthHours_ARestartCarriesOnFromTheSavedHour: the hour in progress
// at a stop is saved, and a server started again in that hour adds to it, so
// the row written at the hour's close has both. A vehicle seen before and
// after the restart is counted once.
func TestFeedHealthHours_ARestartCarriesOnFromTheSavedHour(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	ctx := context.Background()

	before := newFeedHealth(clock.now)
	writer := startHealthHours(ctx, before, store, time.Hour)
	before.record(healthReports, http.StatusCreated)
	before.record(healthReports, http.StatusTooManyRequests)
	sendReport(t, before, liveReportPath, "bus-1", http.StatusCreated)
	sendReport(t, before, liveReportPath, "bus-2", http.StatusCreated)
	writer.Stop()

	clock.advance(10 * time.Minute)
	after := newStoredFeedHealth(t, clock, store)
	sendReport(t, after, liveReportPath, "bus-1", http.StatusCreated)
	after.record(healthFeed, http.StatusOK)

	hours, err := after.hourly(ctx, 1)
	require.NoError(t, err)
	want := healthHour{
		Hour:     healthTestHour,
		Reports:  reportOutcomes{Accepted: 4, RateLimited: 1},
		Feed:     feedOutcomes{Served: 1},
		Vehicles: 2,
	}
	assert.Equal(t, want, hours[len(hours)-1], "the hour in progress shows both servers' counts")

	clock.advance(time.Hour)
	after.saveClosedHours(ctx)
	saved := store.saved()
	require.Len(t, saved, 2)
	assert.Equal(t, want, saved[1])
}

func TestFeedHealthHours_ListsEveryHourWithZerosForTheGaps(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	ctx := context.Background()
	// Saved by an earlier run: 06:00, and 22:00 the day before. 07:00 and
	// 08:00 have no row, as when the server was not running.
	store.rows[healthTestHour.Add(-3*time.Hour)] = healthHour{Hour: healthTestHour.Add(-3 * time.Hour), Reports: reportOutcomes{Accepted: 40}, Vehicles: 4}
	store.rows[healthTestHour.Add(-11*time.Hour)] = healthHour{Hour: healthTestHour.Add(-11 * time.Hour), Feed: feedOutcomes{Served: 7}}
	// Older than the day: not listed.
	store.rows[healthTestHour.Add(-24*time.Hour)] = healthHour{Hour: healthTestHour.Add(-24 * time.Hour), Reports: reportOutcomes{Accepted: 99}}
	h := newStoredFeedHealth(t, clock, store)
	h.record(healthReports, http.StatusCreated)

	hours, err := h.hourly(ctx, 1)
	require.NoError(t, err)
	require.Len(t, hours, healthDayHours)
	for i, hour := range hours {
		assert.Equal(t, healthTestHour.Add(time.Duration(i-healthDayHours+1)*time.Hour), hour.Hour, "oldest first, ending with the hour in progress")
	}
	assert.Equal(t, healthHour{Hour: healthTestHour.Add(-23 * time.Hour)}, hours[0])
	assert.Equal(t, healthHour{Hour: healthTestHour.Add(-11 * time.Hour), Feed: feedOutcomes{Served: 7}}, hours[12])
	assert.Equal(t, healthHour{Hour: healthTestHour.Add(-3 * time.Hour), Reports: reportOutcomes{Accepted: 40}, Vehicles: 4}, hours[20])
	assert.Equal(t, healthHour{Hour: healthTestHour.Add(-2 * time.Hour)}, hours[21], "a gap is zero, not missing")
	assert.Equal(t, healthHour{Hour: healthTestHour.Add(-time.Hour)}, hours[22])
	assert.Equal(t, healthHour{Hour: healthTestHour, Reports: reportOutcomes{Accepted: 1}}, hours[23], "the hour in progress, from memory")

	week, err := h.hourly(ctx, 7)
	require.NoError(t, err)
	require.Len(t, week, healthWeekHours)
	assert.Equal(t, healthHour{Hour: healthTestHour.Add(-24 * time.Hour), Reports: reportOutcomes{Accepted: 99}}, week[healthWeekHours-25])
}

func TestFeedHealthHours_ClosedHoursNotYetSavedAreListed(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	h := newStoredFeedHealth(t, clock, store)

	h.record(healthReports, http.StatusCreated)
	clock.advance(time.Hour)
	hours, err := h.hourly(context.Background(), 1)
	require.NoError(t, err)
	assert.Empty(t, store.saved())
	assert.Equal(t, reportOutcomes{Accepted: 1}, hours[healthDayHours-2].Reports, "the closed hour shows before it is saved")
	assert.Equal(t, healthHour{Hour: healthTestHour.Add(time.Hour)}, hours[healthDayHours-1])
}

// heldHourStore is a fakeHourStore whose saves wait for release. saving gets
// each hour as its save starts.
type heldHourStore struct {
	*fakeHourStore
	saving  chan time.Time
	release chan struct{}
}

func newHeldHourStore() *heldHourStore {
	return &heldHourStore{fakeHourStore: newFakeHourStore(), saving: make(chan time.Time, 1), release: make(chan struct{})}
}

func (s *heldHourStore) SaveFeedHealthHour(ctx context.Context, hour healthHour) error {
	s.saving <- hour.Hour
	<-s.release
	return s.fakeHourStore.SaveFeedHealthHour(ctx, hour)
}

// TestFeedHealthHours_AnHourBeingSavedIsListed: an hour is in memory until the
// store has it, so a slow save doesn't show it as zeros.
func TestFeedHealthHours_AnHourBeingSavedIsListed(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newHeldHourStore()
	h := newStoredFeedHealth(t, clock, store)
	ctx := context.Background()

	for range 2 {
		h.record(healthReports, http.StatusCreated)
		clock.advance(time.Hour)
	}
	saved := make(chan struct{})
	go func() { h.saveClosedHours(ctx); close(saved) }()
	listed := func() []reportOutcomes {
		hours, err := h.hourly(ctx, 1)
		require.NoError(t, err)
		return []reportOutcomes{hours[healthDayHours-3].Reports, hours[healthDayHours-2].Reports}
	}
	both := []reportOutcomes{{Accepted: 1}, {Accepted: 1}}

	for _, hour := range []time.Time{healthTestHour, healthTestHour.Add(time.Hour)} {
		require.Equal(t, hour, <-store.saving)
		assert.Equal(t, both, listed(), "while %s is saved", hour)
		store.release <- struct{}{}
	}
	<-saved
	assert.Equal(t, both, listed(), "once both are saved")
	assert.Len(t, store.saved(), 2, "each hour is saved once")
	h.mu.Lock()
	defer h.mu.Unlock()
	assert.Empty(t, h.closed, "a saved hour leaves memory")
}

// TestFeedHealthHours_SavesWaitForEachOther: a second save waits for the first
// and finds its hour gone, rather than saving it again.
func TestFeedHealthHours_SavesWaitForEachOther(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newHeldHourStore()
	h := newStoredFeedHealth(t, clock, store)
	h.record(healthReports, http.StatusCreated)
	clock.advance(time.Hour)

	var wg sync.WaitGroup
	wg.Go(func() { h.saveClosedHours(context.Background()) })
	require.Equal(t, healthTestHour, <-store.saving)
	wg.Go(func() { h.saveClosedHours(context.Background()) })
	assert.Never(t, func() bool { return len(store.saving) > 0 }, 50*time.Millisecond, time.Millisecond, "the hour is being saved already")
	close(store.release)
	wg.Wait()
	assert.Len(t, store.saved(), 1)
}

// TestFeedHealthHours_AnHourThatGaveWayDuringItsSave: hours that close during a
// save with no room left push the one being saved out, and saving it then
// takes no other hour with it.
func TestFeedHealthHours_AnHourThatGaveWayDuringItsSave(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newHeldHourStore()
	h := newStoredFeedHealth(t, clock, store)

	for range healthWeekHours {
		h.record(healthReports, http.StatusCreated)
		clock.advance(time.Hour)
	}
	saved := make(chan struct{})
	go func() { h.saveClosedHours(context.Background()); close(saved) }()
	require.Equal(t, healthTestHour, <-store.saving)
	h.record(healthReports, http.StatusCreated)
	clock.advance(time.Hour)
	h.record(healthReports, http.StatusCreated)
	store.release <- struct{}{}

	require.Equal(t, healthTestHour.Add(time.Hour), <-store.saving, "the next hour is still there to save")
	close(store.release)
	for range healthWeekHours - 1 {
		<-store.saving
	}
	<-saved
	assert.Len(t, store.saved(), healthWeekHours+1, "every hour is saved, the one that gave way too")
}

func TestFeedHealthHours_AStoreErrorIsReturned(t *testing.T) {
	store := newFakeHourStore()
	h := newStoredFeedHealth(t, newHealthTestClock(healthTestStart), store)
	store.listErr = errors.New("connection refused")

	_, err := h.hourly(context.Background(), 1)
	assert.Error(t, err)
}

func TestFeedHealthHours_ARestoreErrorStartsTheHourEmpty(t *testing.T) {
	store := newFakeHourStore()
	store.listErr = errors.New("connection refused")
	h := newFeedHealth(newHealthTestClock(healthTestStart).now)

	assert.Error(t, h.restoreHours(context.Background(), store))
	h.record(healthReports, http.StatusCreated)
	store.listErr = nil
	hours, err := h.hourly(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, reportOutcomes{Accepted: 1}, hours[healthDayHours-1].Reports, "the store is still used")
}

func TestFeedHealthHours_ConcurrentRecordsAndSaves(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	h := newStoredFeedHealth(t, clock, store)
	ctx := context.Background()

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				h.record(healthReports, http.StatusCreated)
				sendReport(t, h, liveReportPath, "bus-"+string(rune('a'+g)), http.StatusCreated)
				if i%10 == 0 {
					h.saveClosedHours(ctx)
					_, err := h.hourly(ctx, 1)
					assert.NoError(t, err)
				}
			}
		})
	}
	wg.Go(func() {
		for range 5 {
			clock.advance(20 * time.Minute)
		}
	})
	wg.Wait()
	clock.advance(2 * time.Hour)
	h.saveClosedHours(ctx)

	var accepted uint64
	for _, hour := range store.saved() {
		accepted += hour.Reports.Accepted
	}
	assert.Equal(t, uint64(8*50*2), accepted, "every report lands in exactly one saved hour")
}

func TestParseHealthDays(t *testing.T) {
	tests := []struct {
		query string
		want  int
		ok    bool
	}{
		{"", 1, true},
		{"days=1", 1, true},
		{"days=7", 7, true},
		{"days=2", 0, false},
		{"days=0", 0, false},
		{"days=week", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			days, ok := parseHealthDays(httptest.NewRequest(http.MethodGet, "/x?"+tc.query, nil))
			assert.Equal(t, tc.ok, ok)
			if tc.ok {
				assert.Equal(t, tc.want, days)
			}
		})
	}
}

// healthAPIHours is the hours part of GET /api/v1/admin/health's body.
type healthAPIHours struct {
	Hours []struct {
		Hour     string            `json:"hour"`
		Reports  map[string]uint64 `json:"reports"`
		Feed     map[string]uint64 `json:"feed"`
		Vehicles int               `json:"vehicles"`
	} `json:"hours"`
	HoursError *string `json:"hours_error"`
}

func getHealthAPI(t *testing.T, h *feedHealth, query string) (*httptest.ResponseRecorder, healthAPIHours) {
	t.Helper()
	rec := httptest.NewRecorder()
	handleFeedHealth(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/health"+query, nil))
	var body healthAPIHours
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	}
	return rec, body
}

func TestHandleFeedHealth_Hours(t *testing.T) {
	clock := newHealthTestClock(healthTestStart)
	store := newFakeHourStore()
	store.rows[healthTestHour.Add(-2*time.Hour)] = healthHour{Hour: healthTestHour.Add(-2 * time.Hour), Reports: reportOutcomes{Accepted: 12, Invalid: 2}, Vehicles: 3}
	h := newStoredFeedHealth(t, clock, store)
	sendReport(t, h, liveReportPath, "bus-1", http.StatusCreated)

	rec, body := getHealthAPI(t, h, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, body.Hours, healthDayHours)
	assert.Nil(t, body.HoursError)
	assert.Equal(t, "2026-11-13T10:00:00Z", body.Hours[0].Hour)
	assert.Equal(t, map[string]uint64{"accepted": 0, "invalid": 0, "unauthorized": 0, "rate_limited": 0, "server_error": 0}, body.Hours[0].Reports, "an hour with no row is listed with zeros")
	assert.Equal(t, map[string]uint64{"served": 0, "refused": 0, "invalid": 0, "server_error": 0}, body.Hours[0].Feed)
	assert.Equal(t, "2026-11-14T07:00:00Z", body.Hours[21].Hour)
	assert.Equal(t, uint64(12), body.Hours[21].Reports["accepted"])
	assert.Equal(t, uint64(2), body.Hours[21].Reports["invalid"])
	assert.Equal(t, 3, body.Hours[21].Vehicles)
	assert.Equal(t, "2026-11-14T09:00:00Z", body.Hours[23].Hour)
	assert.Equal(t, uint64(1), body.Hours[23].Reports["accepted"])
	assert.Equal(t, 1, body.Hours[23].Vehicles)

	rec, body = getHealthAPI(t, h, "?days=7")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, body.Hours, healthWeekHours)

	rec, _ = getHealthAPI(t, h, "?days=3")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.JSONEq(t, `{"error":"days must be 1 or 7"}`, rec.Body.String())
}

func TestHandleFeedHealth_HoursUnreadable(t *testing.T) {
	store := newFakeHourStore()
	h := newStoredFeedHealth(t, newHealthTestClock(healthTestStart), store)
	h.record(healthReports, http.StatusCreated)
	store.listErr = errors.New("connection refused")

	rec, body := getHealthAPI(t, h, "")
	require.Equal(t, http.StatusOK, rec.Code, "the minutes are in memory and still answer")
	assert.Empty(t, body.Hours)
	assert.NotNil(t, body.Hours, "an empty list, not null")
	require.NotNil(t, body.HoursError)
	assert.Equal(t, "the hourly counts could not be read from the database", *body.HoursError)
	assert.Contains(t, rec.Body.String(), `"accepted":1`, "the last hour is still there")
}
