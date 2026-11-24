package main

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// failedSignInWindow is how long after a failed sign-in further ones for
	// the same account, from the same address and through the same way in,
	// are counted rather than written one by one.
	failedSignInWindow = 10 * time.Minute
	// failedSignInSweep is how often the ended windows are looked for, so a
	// count is written at most this long after its window ends.
	failedSignInSweep = 30 * time.Second
	// failedSignInQueue is how many failed sign-ins may wait for the worker.
	// Each sign-in limiter lets loginEmailLimit a minute through for an
	// account, so it fills only while the database is slow or not answering.
	failedSignInQueue = 256
	// failedSignInWriteTimeout bounds each row, and failedSignInFlushTimeout
	// everything Stop has left to write.
	failedSignInWriteTimeout = 5 * time.Second
	failedSignInFlushTimeout = 5 * time.Second
)

// failedSignInAudit is what the audit log keeps of failed sign-ins: which way
// in, and how many the row is for. Never the email or the password typed.
type failedSignInAudit struct {
	Via      string `json:"via"`
	Attempts int    `json:"attempts"`
}

// failedSignIn is an account that could not be signed in to, the address that
// tried and the way in it tried. Repeats of one are counted together.
type failedSignIn struct {
	userID int64
	ip     string
	via    string
}

// FailedSignInNoter notes a failed sign-in for the audit log without waiting
// for its row to be written.
type FailedSignInNoter interface {
	NoteFailedSignIn(user *User, ip, via string)
}

// FailedSignInStore writes the row for attempts failed sign-ins.
type FailedSignInStore interface {
	RecordFailedSignIns(ctx context.Context, f failedSignIn, attempts int) error
}

var _ FailedSignInStore = (*Store)(nil)

// RecordFailedSignIns records attempts failed sign-ins against the address
// they came from. Who was at it is unknown, so the row has no actor.
func (s *Store) RecordFailedSignIns(ctx context.Context, f failedSignIn, attempts int) error {
	ctx = context.WithValue(ctx, auditActorKey, auditActor{IP: f.ip})
	return recordAudit(ctx, s.queries, auditChange{
		ObjectType: auditSession, ObjectID: strconv.FormatInt(f.userID, 10), Action: auditSignInFail,
		After: failedSignInAudit{Via: f.via, Attempts: attempts},
	})
}

// failedWindow is a window still counting: when its first failure was, which
// has its row already, and how many have followed.
type failedWindow struct {
	opened time.Time
	more   int
}

// failedSignInRecorder writes failed sign-ins to the audit log off the request
// path. Written on it, a wrong password for an admin's email would take longer
// to answer than one for an email no account has, which writes nothing, and
// the sign-in form would tell an attacker which emails exist. So the request
// only queues the failure, and the worker writes it. That is why these rows,
// unlike every other, can be lost: in a crash, or when the queue is full.
//
// The first failure is written at once. Further ones in failedSignInWindow are
// counted and written as one row when it ends, so an attack that keeps on adds
// two rows a window for each account, address and way in, not one a try.
//
// The worker owns its context, and windows belongs to it alone.
type failedSignInRecorder struct {
	store   FailedSignInStore
	now     func() time.Time
	queue   chan failedSignIn
	dropped atomic.Int64
	windows map[failedSignIn]*failedWindow

	flushTimeout time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

var _ FailedSignInNoter = (*failedSignInRecorder)(nil)

// newFailedSignInRecorder starts the worker. now is its clock, and sweep ticks
// when it should look for ended windows.
func newFailedSignInRecorder(store FailedSignInStore, now func() time.Time, sweep <-chan time.Time) *failedSignInRecorder {
	// A background job outlives any single request, so it owns its context
	// rather than borrowing a request's.
	ctx, cancel := context.WithCancel(context.Background())
	r := &failedSignInRecorder{
		store:        store,
		now:          now,
		queue:        make(chan failedSignIn, failedSignInQueue),
		windows:      make(map[failedSignIn]*failedWindow),
		flushTimeout: failedSignInFlushTimeout,
		ctx:          ctx,
		cancel:       cancel,
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	go r.run(sweep)
	return r
}

// NoteFailedSignIn queues a failed sign-in to user for the worker. It never
// waits: with the queue full, the failure is dropped and counted, and the
// worker logs the count. A role that may not use the admin UI is not recorded,
// as its sign-ins are not.
func (r *failedSignInRecorder) NoteFailedSignIn(user *User, ip, via string) {
	if !adminUIRole(user.Role) {
		return
	}
	select {
	case r.queue <- failedSignIn{userID: user.ID, ip: ip, via: via}:
	default:
		r.dropped.Add(1)
	}
}

// Stop writes what is queued and counted and waits for the worker to exit,
// for failedSignInFlushTimeout at most: writes still going then are cancelled
// and what is left is lost. Safe to call more than once.
func (r *failedSignInRecorder) Stop() {
	r.once.Do(func() {
		deadline := time.AfterFunc(r.flushTimeout, r.cancel)
		close(r.stop)
		<-r.done
		deadline.Stop()
		r.cancel()
	})
	<-r.done
}

func (r *failedSignInRecorder) run(sweep <-chan time.Time) {
	defer close(r.done)

	for {
		select {
		case f := <-r.queue:
			r.count(f)
		case <-sweep:
			r.writeEnded()
		case <-r.stop:
			r.flush()
			return
		}
	}
}

// count adds one failure: to its window when one is open, and as a row of its
// own, opening one, when none is.
func (r *failedSignInRecorder) count(f failedSignIn) {
	now := r.now()
	if w, ok := r.windows[f]; ok {
		if now.Sub(w.opened) < failedSignInWindow {
			w.more++
			return
		}
		r.close(f, w)
	}
	// Bounded as the sign-in limiters' maps are.
	if len(r.windows) >= maxTrackedLogins {
		r.dropped.Add(1)
		return
	}
	r.windows[f] = &failedWindow{opened: now}
	r.write(f, 1)
}

// close ends a window, writing the failures that followed its first.
func (r *failedSignInRecorder) close(f failedSignIn, w *failedWindow) {
	delete(r.windows, f)
	if w.more > 0 {
		r.write(f, w.more)
	}
}

// writeEnded closes every window that has ended, and says how many failures
// were dropped since it last did.
func (r *failedSignInRecorder) writeEnded() {
	now := r.now()
	for f, w := range r.windows {
		if now.Sub(w.opened) >= failedSignInWindow {
			r.close(f, w)
		}
	}
	r.logDropped()
}

// flush writes everything queued and counted, until Stop's deadline.
func (r *failedSignInRecorder) flush() {
	for len(r.queue) > 0 && r.ctx.Err() == nil {
		r.count(<-r.queue)
	}
	for f, w := range r.windows {
		if r.ctx.Err() != nil {
			break
		}
		r.close(f, w)
	}
	if r.ctx.Err() != nil {
		slog.Error("audit log: failed sign-ins lost, they could not all be written before the server stopped")
	}
	r.logDropped()
}

func (r *failedSignInRecorder) logDropped() {
	if n := r.dropped.Swap(0); n > 0 {
		slog.Warn("audit log: failed sign-ins dropped, too many to record", "dropped", n)
	}
}

func (r *failedSignInRecorder) write(f failedSignIn, attempts int) {
	ctx, cancel := context.WithTimeout(r.ctx, failedSignInWriteTimeout)
	defer cancel()
	if err := r.store.RecordFailedSignIns(ctx, f, attempts); err != nil {
		slog.Error("audit log: failed sign-ins not recorded", "sub", f.userID, "attempts", attempts, "error", err)
	}
}
