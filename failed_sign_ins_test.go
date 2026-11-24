package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/OneBusAway/vehicle-positions/rider"
)

// recordedFailure is one row a fakeFailedSignInStore was told to write.
type recordedFailure struct {
	failedSignIn
	attempts int
}

// fakeFailedSignInStore keeps the rows it was told to write. While hold is
// not nil, a write waits for it to close or for its context to end; errs are
// what the first writes return, one each.
type fakeFailedSignInStore struct {
	hold chan struct{}

	mu      sync.Mutex
	started int
	rows    []recordedFailure
	errs    []error
}

func (f *fakeFailedSignInStore) RecordFailedSignIns(ctx context.Context, s failedSignIn, attempts int) error {
	f.mu.Lock()
	f.started++
	f.mu.Unlock()
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return err
	}
	f.rows = append(f.rows, recordedFailure{s, attempts})
	return nil
}

func (f *fakeFailedSignInStore) recorded() []recordedFailure {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedFailure{}, f.rows...)
}

// attempts is what each row says happened how many times, in order.
func (f *fakeFailedSignInStore) attempts() []int {
	counts := make([]int, 0)
	for _, row := range f.recorded() {
		counts = append(counts, row.attempts)
	}
	return counts
}

// tries is how many writes have been started.
func (f *fakeFailedSignInStore) tries() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

func (f *fakeFailedSignInStore) writing() bool { return f.tries() > 0 }

// recorderHarness is a recorder over a fake store, with its clock and its
// sweep in the test's hands.
type recorderHarness struct {
	*failedSignInRecorder
	store *fakeFailedSignInStore
	clock *healthTestClock
	sweep chan time.Time
}

func newRecorderHarness(t *testing.T, store *fakeFailedSignInStore) *recorderHarness {
	t.Helper()
	h := &recorderHarness{store: store, clock: newHealthTestClock(healthTestStart), sweep: make(chan time.Time)}
	h.failedSignInRecorder = newFailedSignInRecorder(store, h.clock.now, h.sweep)
	t.Cleanup(h.Stop)
	return h
}

// settle returns once the worker has dealt with everything noted so far. The
// sweep channel has no buffer, so the worker takes a tick only between jobs.
func (h *recorderHarness) settle(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return len(h.queue) == 0 }, 5*time.Second, time.Millisecond)
	h.tick()
}

// tick runs one sweep and returns once it is over: the worker takes the second
// tick only then.
func (h *recorderHarness) tick() {
	h.sweep <- time.Time{}
	h.sweep <- time.Time{}
}

// closed reports whether ch is closed.
func closed(ch <-chan struct{}) func() bool {
	return func() bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}
}

var (
	failingAdmin  = &User{ID: 9, Role: roleAdmin}
	failingViewer = &User{ID: 10, Role: roleViewer}
)

// TestFailedSignIns_FirstAtOnceThenCounted: the first failure has a row
// straight away, and those in the ten minutes after it one row between them,
// once the ten minutes are over. Then it starts again.
func TestFailedSignIns_FirstAtOnceThenCounted(t *testing.T) {
	h := newRecorderHarness(t, &fakeFailedSignInStore{})
	fail := func() { h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAdmin) }

	fail()
	h.settle(t)
	want := failedSignIn{userID: 9, ip: "198.51.100.9", via: auditViaAdmin}
	assert.Equal(t, []recordedFailure{{want, 1}}, h.store.recorded(), "written at once")

	for range 37 {
		fail()
	}
	h.clock.advance(failedSignInWindow - time.Second)
	h.settle(t)
	assert.Equal(t, []int{1}, h.store.attempts(), "counted until the window ends")

	h.clock.advance(time.Second)
	h.tick()
	assert.Equal(t, []recordedFailure{{want, 1}, {want, 37}}, h.store.recorded())

	fail()
	h.settle(t)
	assert.Equal(t, []int{1, 37, 1}, h.store.attempts(), "the next has a row of its own")
}

// TestFailedSignIns_WindowThatEndedUnswept: a failure after the window ended,
// before the sweep has looked, isn't counted in it.
func TestFailedSignIns_WindowThatEndedUnswept(t *testing.T) {
	h := newRecorderHarness(t, &fakeFailedSignInStore{})
	fail := func() { h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAPI) }
	fail()
	fail()
	fail()
	h.settle(t)

	h.clock.advance(failedSignInWindow)
	fail()
	h.settle(t)

	assert.Equal(t, []int{1, 2, 1}, h.store.attempts())
}

// TestFailedSignIns_OneFailureHasOneRow: a window nothing followed adds no
// row when it ends.
func TestFailedSignIns_OneFailureHasOneRow(t *testing.T) {
	h := newRecorderHarness(t, &fakeFailedSignInStore{})
	h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAdmin)
	h.settle(t)
	h.clock.advance(failedSignInWindow)
	h.tick()
	h.Stop()
	assert.Equal(t, []int{1}, h.store.attempts())
	assert.Empty(t, h.windows, "the window is let go")
}

// TestFailedSignIns_CountedApart: another account, address or way in is
// another window, with a first row of its own.
func TestFailedSignIns_CountedApart(t *testing.T) {
	h := newRecorderHarness(t, &fakeFailedSignInStore{})
	h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAdmin)
	h.NoteFailedSignIn(failingViewer, "198.51.100.9", auditViaAdmin)
	h.NoteFailedSignIn(failingAdmin, "198.51.100.10", auditViaAdmin)
	h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAPI)
	h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAdmin)
	h.settle(t)

	assert.Equal(t, []recordedFailure{
		{failedSignIn{9, "198.51.100.9", auditViaAdmin}, 1},
		{failedSignIn{10, "198.51.100.9", auditViaAdmin}, 1},
		{failedSignIn{9, "198.51.100.10", auditViaAdmin}, 1},
		{failedSignIn{9, "198.51.100.9", auditViaAPI}, 1},
	}, h.store.recorded())
}

func TestFailedSignIns_DriversAreNotRecorded(t *testing.T) {
	h := newRecorderHarness(t, &fakeFailedSignInStore{})
	for _, role := range []string{roleDriver, roleRider, ""} {
		h.NoteFailedSignIn(&User{ID: 11, Role: role}, "198.51.100.9", auditViaAPI)
	}
	h.Stop()
	assert.Empty(t, h.store.recorded())
}

// TestFailedSignIns_FullQueueDoesNotWait: with the database not answering and
// the queue full, noting another failure returns at once, and the log says
// once how many were dropped.
// Not safe for t.Parallel(); uses global logger.
func TestFailedSignIns_FullQueueDoesNotWait(t *testing.T) {
	logs := captureLogs(t)
	store := &fakeFailedSignInStore{hold: make(chan struct{})}
	h := newRecorderHarness(t, store)
	fail := func() { h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAdmin) }
	fail()
	require.Eventually(t, store.writing, 5*time.Second, time.Millisecond, "the worker waits on the first row")

	noted := make(chan struct{})
	go func() {
		defer close(noted)
		for range failedSignInQueue + 5 {
			fail()
		}
	}()
	require.Eventually(t, closed(noted), 5*time.Second, time.Millisecond, "noting a failure waited for the store")

	close(store.hold)
	h.settle(t)
	h.settle(t)
	assert.Equal(t, 1, strings.Count(logs.String(), "failed sign-ins dropped"), logs.String())
	assert.Contains(t, logs.String(), "dropped=5")

	h.Stop()
	assert.Equal(t, []int{1, failedSignInQueue}, store.attempts(), "what was queued is counted")
}

// TestFailedSignIns_Bounded: with as many windows open as the sign-in
// limiters track, a failure that would open another is dropped, and one whose
// window is open is still counted.
// Not safe for t.Parallel(); uses global logger.
func TestFailedSignIns_Bounded(t *testing.T) {
	logs := captureLogs(t)
	h := newRecorderHarness(t, &fakeFailedSignInStore{})
	for i := range maxTrackedLogins - 1 {
		h.windows[failedSignIn{userID: int64(100 + i), ip: "203.0.113.5", via: auditViaAPI}] = &failedWindow{opened: healthTestStart}
	}
	h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAdmin)
	h.NoteFailedSignIn(failingViewer, "198.51.100.9", auditViaAdmin)
	h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAdmin)
	h.settle(t)
	assert.Contains(t, logs.String(), "dropped=1")

	h.Stop()
	want := failedSignIn{9, "198.51.100.9", auditViaAdmin}
	assert.Equal(t, []recordedFailure{{want, 1}, {want, 1}}, h.store.recorded(),
		"the last window there was room for, and the count of the one that followed in it")
}

// TestFailedSignIns_WriteThatFails: a row the store refuses is logged with the
// account and the count, and the worker goes on to the next.
// Not safe for t.Parallel(); uses global logger.
func TestFailedSignIns_WriteThatFails(t *testing.T) {
	logs := captureLogs(t)
	h := newRecorderHarness(t, &fakeFailedSignInStore{errs: []error{errors.New("connection refused")}})
	h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAdmin)
	h.NoteFailedSignIn(failingViewer, "198.51.100.9", auditViaAdmin)
	h.settle(t)

	assert.Contains(t, logs.String(), `level=ERROR msg="audit log: failed sign-ins not recorded" sub=9 attempts=1 error="connection refused"`)
	assert.Equal(t, []recordedFailure{{failedSignIn{10, "198.51.100.9", auditViaAdmin}, 1}}, h.store.recorded())
}

// TestFailedSignIns_StopFlushes: what is queued and what is counted is written
// before Stop returns.
func TestFailedSignIns_StopFlushes(t *testing.T) {
	store := &fakeFailedSignInStore{hold: make(chan struct{})}
	h := newRecorderHarness(t, store)
	for range 4 {
		h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAdmin)
	}
	h.NoteFailedSignIn(failingViewer, "198.51.100.9", auditViaAPI)
	require.Eventually(t, store.writing, 5*time.Second, time.Millisecond)

	stopped := make(chan struct{})
	go func() { h.Stop(); close(stopped) }()
	require.Eventually(t, closed(h.stop), 5*time.Second, time.Millisecond)
	close(store.hold)
	<-stopped

	admin, viewer := failedSignIn{9, "198.51.100.9", auditViaAdmin}, failedSignIn{10, "198.51.100.9", auditViaAPI}
	assert.ElementsMatch(t, []recordedFailure{{admin, 1}, {viewer, 1}, {admin, 3}}, store.recorded())
	h.Stop()
}

// TestFailedSignIns_StopIsBounded: Stop gives up on a database that does not
// answer, so it can't hold up the shutdown, and says rows were lost.
// Not safe for t.Parallel(); uses global logger.
func TestFailedSignIns_StopIsBounded(t *testing.T) {
	logs := captureLogs(t)
	store := &fakeFailedSignInStore{hold: make(chan struct{})}
	h := newRecorderHarness(t, store)
	h.flushTimeout = 10 * time.Millisecond
	h.NoteFailedSignIn(failingAdmin, "198.51.100.9", auditViaAdmin)
	require.Eventually(t, store.writing, 5*time.Second, time.Millisecond)

	stopped := make(chan struct{})
	go func() { h.Stop(); close(stopped) }()

	require.Eventually(t, closed(stopped), failedSignInWriteTimeout-time.Second, time.Millisecond, "Stop waited for the write")
	assert.Empty(t, store.recorded())
	assert.Contains(t, logs.String(), "failed sign-ins lost")
}

// TestFailedSignIns_StopDuringASweep: once Stop's deadline has passed, a sweep
// under way tries no more writes, which would each fail and be logged, and the
// rows it had left are said to be lost once.
// Not safe for t.Parallel(); uses global logger.
func TestFailedSignIns_StopDuringASweep(t *testing.T) {
	logs := captureLogs(t)
	store := &fakeFailedSignInStore{}
	h := newRecorderHarness(t, store)
	h.flushTimeout = 10 * time.Millisecond
	for _, ip := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		h.NoteFailedSignIn(failingAdmin, ip, auditViaAdmin)
		h.NoteFailedSignIn(failingAdmin, ip, auditViaAdmin)
	}
	h.settle(t)
	require.Equal(t, []int{1, 1, 1}, store.attempts())

	// The worker is idle until the tick, and takes these with it.
	store.hold = make(chan struct{})
	h.clock.advance(failedSignInWindow)
	h.sweep <- time.Time{}
	require.Eventually(t, func() bool { return store.tries() == 4 }, 5*time.Second, time.Millisecond)
	h.Stop()

	assert.Equal(t, 4, store.tries(), "the write in hand is the last")
	assert.Equal(t, 1, strings.Count(logs.String(), "failed sign-ins not recorded"))
	assert.Equal(t, 1, strings.Count(logs.String(), "failed sign-ins lost"))
}

// signInWays are the two ways in, each as a function that tries email and
// password on a server whose accounts are users, tries times, and returns the
// status of each answer.
var signInWays = []struct {
	name, via string
	try       func(t *testing.T, users UserFetcher, failures FailedSignInNoter, tries int, email, password string) []int
}{
	{"API", auditViaAPI, func(t *testing.T, users UserFetcher, failures FailedSignInNoter, tries int, email, password string) []int {
		limiter := NewLoginRateLimiter()
		t.Cleanup(limiter.Stop)
		handler := handleLogin(users, newFakeRefreshTokens(), failures, testSecret, testTTLs, limiter, true)
		codes := make([]int, 0, tries)
		for range tries {
			body, err := json.Marshal(LoginRequest{Email: email, Password: password})
			require.NoError(t, err)
			w := httptest.NewRecorder()
			handler(w, sentThroughProxy(httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))))
			codes = append(codes, w.Code)
		}
		return codes
	}},
	{"admin site", auditViaAdmin, func(t *testing.T, users UserFetcher, failures FailedSignInNoter, tries int, email, password string) []int {
		ui := newTestAdminUI(t)
		ui.cfg.trustProxy = true
		ui.users, ui.failedSignIns = users, failures
		mux := http.NewServeMux()
		registerAdminUI(mux, ui)
		codes := make([]int, 0, tries)
		for range tries {
			form := url.Values{"email": {email}, "password": {password}}
			req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, sentThroughProxy(req))
			codes = append(codes, w.Code)
		}
		return codes
	}},
}

// failingUsers is a UserFetcher that can't look anyone up.
type failingUsers struct{ fakeUserFetcher }

func (failingUsers) GetUserByEmail(context.Context, string) (*User, error) {
	return nil, errors.New("connection refused")
}

// TestFailedSignIns_WhichAreRecorded tries each kind of sign-in that can fail,
// both ways in, through the real recorder.
func TestFailedSignIns_WhichAreRecorded(t *testing.T) {
	deactivated := func(role string) *User {
		u := sessionAccount(t, role)
		u.Active = false
		return u
	}
	unreadable := sessionAccount(t, roleAdmin)
	unreadable.PasswordHash = "not a hash"
	tests := []struct {
		name     string
		user     *User
		email    string
		password string
		tries    int
		want     []int // the attempts in each row
	}{
		{"an admin's wrong password", sessionAccount(t, roleAdmin), "admin@test.com", "nope", 1, []int{1}},
		{"a viewer's wrong password", sessionAccount(t, roleViewer), "viewer@test.com", "nope", 1, []int{1}},
		{"a deactivated admin's right password", deactivated(roleAdmin), "admin@test.com", sessionPassword, 1, []int{1}},
		{"a deactivated viewer's wrong password", deactivated(roleViewer), "viewer@test.com", "nope", 1, []int{1}},
		{"an unknown email", sessionAccount(t, roleAdmin), "ghost@test.com", "nope", 1, []int{}},
		{"a driver's wrong password", sessionAccount(t, roleDriver), "driver@test.com", "nope", 1, []int{}},
		{"a deactivated driver", deactivated(roleDriver), "driver@test.com", sessionPassword, 1, []int{}},
		{"the right password", sessionAccount(t, roleAdmin), "admin@test.com", sessionPassword, 1, []int{}},
		{"a driver's right password", sessionAccount(t, roleDriver), "driver@test.com", sessionPassword, 1, []int{}},
		{"a hash that can't be read", unreadable, "admin@test.com", sessionPassword, 1, []int{}},
		{"tries past the limit", sessionAccount(t, roleAdmin), "admin@test.com", "nope", loginEmailLimit + 3, []int{1, loginEmailLimit - 1}},
	}
	for _, way := range signInWays {
		for _, tc := range tests {
			t.Run(way.name+": "+tc.name, func(t *testing.T) {
				h := newRecorderHarness(t, &fakeFailedSignInStore{})
				users := &fakeUserFetcher{users: map[string]*User{tc.user.Email: tc.user}}

				codes := way.try(t, users, h, tc.tries, tc.email, tc.password)

				if len(tc.want) > 0 {
					require.Equal(t, http.StatusUnauthorized, codes[0])
				}
				if tc.tries > 1 {
					require.Equal(t, http.StatusTooManyRequests, codes[len(codes)-1], "the limit was reached")
				}
				h.Stop()
				assert.Equal(t, tc.want, h.store.attempts())
				for _, row := range h.store.recorded() {
					assert.Equal(t, failedSignIn{userID: 9, ip: "198.51.100.9", via: way.via}, row.failedSignIn)
				}
			})
		}

		t.Run(way.name+": the database can't be read", func(t *testing.T) {
			h := newRecorderHarness(t, &fakeFailedSignInStore{})
			codes := way.try(t, &failingUsers{}, h, 1, "admin@test.com", "nope")
			require.Equal(t, []int{http.StatusInternalServerError}, codes)
			h.Stop()
			assert.Empty(t, h.store.recorded())
		})
	}
}

// TestFailedSignIns_AnswerDoesNotWaitForTheRow: the sign-in is answered while
// the database holds the row up. Waiting for it is what would tell an admin's
// email from an unknown one.
func TestFailedSignIns_AnswerDoesNotWaitForTheRow(t *testing.T) {
	for _, way := range signInWays {
		t.Run(way.name, func(t *testing.T) {
			store := &fakeFailedSignInStore{hold: make(chan struct{})}
			h := newRecorderHarness(t, store)
			user := sessionAccount(t, roleAdmin)
			users := &fakeUserFetcher{users: map[string]*User{user.Email: user}}

			answered := make(chan []int, 1)
			go func() { answered <- way.try(t, users, h, 3, user.Email, "nope") }()

			require.Eventually(t, func() bool { return len(answered) == 1 }, 5*time.Second, time.Millisecond, "the answer waited")
			assert.Equal(t, []int{http.StatusUnauthorized, http.StatusUnauthorized, http.StatusUnauthorized}, <-answered)
			assert.Empty(t, store.recorded(), "no row is written yet")
			require.Eventually(t, store.writing, 5*time.Second, time.Millisecond)
			close(store.hold)
			h.Stop()
			assert.Equal(t, []int{1, 2}, store.attempts())
		})
	}
}

// TestAuditPage_FailedSignInRows checks a failed sign-in, and a count of them,
// are by no one known, and that the server's own rows still name it.
func TestAuditPage_FailedSignInRows(t *testing.T) {
	at := time.Date(2026, 11, 24, 14, 5, 0, 0, time.UTC)
	log := &fakeAuditLog{entries: []auditEntry{
		{
			ID: 4, At: at, ActorIP: "198.51.100.9", ObjectType: auditSession, ObjectID: "7", Action: auditSignInFail, AccountName: "Ana Diaz",
			After: fieldsJSON(t, failedSignInAudit{Via: auditViaAdmin, Attempts: 37}),
		},
		{
			ID: 3, At: at, ActorIP: "198.51.100.9", ObjectType: auditSession, ObjectID: "7", Action: auditSignInFail, AccountName: "Ana Diaz",
			After: fieldsJSON(t, failedSignInAudit{Via: auditViaAdmin, Attempts: 1}),
		},
		{
			ID: 2, At: at, ActorIP: "198.51.100.10", ObjectType: auditSession, ObjectID: "8", Action: auditSignInFail,
			After: fieldsJSON(t, failedSignInAudit{Via: auditViaAPI, Attempts: 2}),
		},
		{
			ID: 1, At: at, ObjectType: auditUser, ObjectID: "5", Action: auditCreate,
			After: fieldsJSON(t, userAudit{Name: "Administrator", Email: "boot@example.org", Role: roleAdmin, Active: true}),
		},
	}}
	mux := newAuditMux(t, log)
	body := getAdminPage(t, mux, "/admin/audit")

	assert.Contains(t, body, `37 failed sign-ins as <a href="/admin/audit?id=7&amp;type=session">Ana Diaz</a>`)
	assert.Contains(t, body, `Failed sign-in as <a href="/admin/audit?id=7&amp;type=session">Ana Diaz</a>`)
	assert.Contains(t, body, `2 failed sign-ins as <a href="/admin/audit?id=8&amp;type=session">account 8</a>`)
	assert.Equal(t, 2, strings.Count(body, `<span class="muted">Unknown</span><span class="sub num">198.51.100.9</span>`))
	assert.Equal(t, 1, strings.Count(body, `<span class="muted">Server</span>`), "the account the server made, and nothing else")
	assert.Equal(t, 2, strings.Count(body, `<span class="muted">Through</span> Admin site`))

	req := httptest.NewRequest(http.MethodGet, "/admin/audit", nil)
	req.AddCookie(cookieFor(t, roleViewer))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `<span class="muted">Unknown</span></td>`)
	assert.NotContains(t, w.Body.String(), "198.51.100.")
}

// TestStore_RecordFailedSignIns pins the row: about the account, by no one,
// from the address.
func TestStore_RecordFailedSignIns(t *testing.T) {
	store := newAuditTestStore(t)
	fx := newAuditFixture(t, store)
	mark := lastAuditID(t, store)

	f := failedSignIn{userID: fx.userID, ip: "198.51.100.9", via: auditViaAdmin}
	require.NoError(t, store.RecordFailedSignIns(context.Background(), f, 37))

	rows := auditRowsAfter(t, store, mark)
	require.Len(t, rows, 1)
	assert.JSONEq(t, `{"via":"admin","attempts":37}`, rows[0].After)
	rows[0].After = ""
	assert.Equal(t, storedAuditRow{ActorIP: "198.51.100.9", ObjectType: auditSession, ObjectID: idText(fx.userID), Action: auditSignInFail}, rows[0])

	entries, err := store.ListAuditEntries(context.Background(), auditFilter{ObjectType: auditSession, ObjectID: idText(fx.userID), Limit: 10})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Nil(t, entries[0].ActorID)
	assert.Equal(t, "Ana", entries[0].AccountName)
}

// TestAuditLog_FailedSignInsThroughTheServer is the whole path against the
// database: wrong passwords both ways in, read back on the page and through
// the API, with nothing that was typed kept.
func TestAuditLog_FailedSignInsThroughTheServer(t *testing.T) {
	store := newAuditTestStore(t)
	ctx := context.Background()
	admin, err := store.CreateUser(ctx, "Ana Diaz", "aud-ana@example.org", sessionPassword, roleAdmin)
	require.NoError(t, err)
	_, err = store.CreateUser(ctx, "Dee Driver", "aud-dee@example.org", sessionPassword, roleDriver)
	require.NoError(t, err)

	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	ll := NewLoginRateLimiter()
	t.Cleanup(ll.Stop)
	adminLL := NewLoginRateLimiter()
	t.Cleanup(adminLL.Stop)
	recorder := newFailedSignInRecorder(store, time.Now, nil)
	t.Cleanup(recorder.Stop)
	reports := newServiceReporter(&fakeReportStore{}, func() *rider.Index { return nil }, testReportSettings)
	h, err := newHandler(serverStore{store, recorder}, tracker, nil, ll, adminLL, nil, testSecret, testTTLs, time.Now(),
		adminUIConfig{enabled: true, trustProxy: true, stalenessThreshold: 5 * time.Minute}, false, "", nil, nil, nil,
		newAlertWatcher(store, testAlertSettings), reports, newFeedHealth(time.Now))
	require.NoError(t, err)
	serve := func(req *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	const typed = "Tr0ub4dor-typed"
	tryAPI := func(email string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"`+email+`","password":"`+typed+`"}`))
		req.Header.Set("X-Forwarded-For", "198.51.100.51")
		require.Equal(t, http.StatusUnauthorized, serve(req).Code)
	}
	trySite := func(email string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(url.Values{"email": {email}, "password": {typed}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Forwarded-For", "198.51.100.52")
		require.Equal(t, http.StatusUnauthorized, serve(req).Code)
	}

	mark := lastAuditID(t, store)
	tryAPI("AUD-ana@example.org")
	for range 3 {
		trySite("AUD-ana@example.org")
	}
	for _, email := range []string{"aud-dee@example.org", "aud-ghost@example.org", typed + "@example.org"} {
		tryAPI(email)
		trySite(email)
	}
	recorder.Stop()

	got := make([]string, 0)
	for _, r := range auditRowsAfter(t, store, mark) {
		assert.Nil(t, r.ActorID)
		assert.Empty(t, r.ActorEmail)
		assert.Equal(t, idText(admin.ID), r.ObjectID)
		got = append(got, r.ActorIP+" "+r.Action+" "+r.After)
	}
	assert.Equal(t, []string{
		`198.51.100.51 sign_in_failed {"via": "api", "attempts": 1}`,
		`198.51.100.52 sign_in_failed {"via": "admin", "attempts": 1}`,
		`198.51.100.52 sign_in_failed {"via": "admin", "attempts": 2}`,
	}, got)
	var kept int
	require.NoError(t, store.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log a WHERE a::text ILIKE '%Tr0ub4dor%'`).Scan(&kept))
	assert.Zero(t, kept, "nothing typed is kept")

	session, err := generateJWT(&User{ID: admin.ID, Email: admin.Email, Role: roleAdmin}, testSecret, sessionLifetime)
	require.NoError(t, err)
	read := func(path string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		w := serve(req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		return w.Body.String()
	}
	query := "?type=session&id=" + idText(admin.ID)
	page := read("/admin/audit" + query)
	assert.Contains(t, page, "Sign-ins for Ana Diaz")
	assert.Contains(t, page, "2 failed sign-ins as <a")
	assert.Contains(t, page, `<span class="muted">Unknown</span><span class="sub num">198.51.100.52</span>`)
	api := read("/api/v1/admin/audit" + query)
	assert.Contains(t, api, `"actor_id":null,"actor_email":"","actor_ip":"198.51.100.52"`)
	assert.Contains(t, read("/api/v1/admin/audit?actor="+idText(admin.ID)), `"entries":[]`, "no one knows they were this admin's")
}
