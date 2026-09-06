package rider

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	gtfsrt "github.com/OneBusAway/go-gtfs/proto"
	"google.golang.org/protobuf/proto"
)

const (
	// trustedRequestTimeout bounds a single GTFS-RT fetch.
	trustedRequestTimeout = 15 * time.Second
	// trustedMaxBodyBytes caps how much of a feed response we are willing to read.
	trustedMaxBodyBytes = 32 << 20
)

// FeedHealth reports the state of one configured trusted feed URL.
type FeedHealth struct {
	URL         string
	LastSuccess time.Time
	LastError   string
	Entities    int
}

// feedState is the poller's private state for a single URL.
type feedState struct {
	url          string
	etag         string
	lastModified string
	entities     map[TripKey]TrustedVehicle
	lastSuccess  time.Time
	lastError    string
}

// TrustedFeed polls one or more GTFS-RT VehiclePositions feeds and answers
// "where does the agency think this trip is right now?".
type TrustedFeed struct {
	client *http.Client
	maxAge time.Duration

	mu    sync.RWMutex
	feeds []*feedState
}

// NewTrustedFeed builds a poller over urls. maxAge is how old a vehicle
// position may be before Lookup ignores it.
func NewTrustedFeed(urls []string, client *http.Client, maxAge time.Duration) *TrustedFeed {
	if client == nil {
		client = http.DefaultClient
	}
	f := &TrustedFeed{client: client, maxAge: maxAge}
	for _, u := range urls {
		f.feeds = append(f.feeds, &feedState{url: u, entities: map[TripKey]TrustedVehicle{}})
	}
	return f
}

// Configured reports whether any feed URL was supplied.
func (f *TrustedFeed) Configured() bool { return len(f.feeds) > 0 }

// Poll fetches every configured feed once, concurrently: a slow or hanging
// feed must not delay the others, and each feed's state is written under the
// same lock, so the fetches are independent. Completion order does not matter
// — Lookup resolves a key that several feeds carry by configuration order, not
// by which answered last. It never returns an error: per-feed failures are
// recorded in Health and leave that feed's previously fetched entities in
// place.
func (f *TrustedFeed) Poll(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	var wg sync.WaitGroup
	for _, feed := range f.feeds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.pollOne(ctx, feed)
		}()
	}
	wg.Wait()
}

// Start polls immediately and then every `every` until ctx is done.
func (f *TrustedFeed) Start(ctx context.Context, every time.Duration) {
	f.Poll(ctx)
	TickUntilDone(ctx, every, func(time.Time) { f.Poll(ctx) })
}

// pollOne fetches a single feed and folds the result into its state.
func (f *TrustedFeed) pollOne(ctx context.Context, feed *feedState) {
	f.mu.RLock()
	etag, lastModified := feed.etag, feed.lastModified
	f.mu.RUnlock()

	got, err := f.fetch(ctx, feed.url, etag, lastModified)
	now := time.Now()

	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		feed.lastError = err.Error()
		slog.Warn("rider: trusted feed poll failed, keeping the previous vehicles", "url", feed.url, "error", err)
		return
	}
	feed.lastError = ""
	feed.lastSuccess = now
	if got.notModified {
		return
	}
	feed.entities = got.entities
	feed.etag = got.etag
	feed.lastModified = got.lastModified
}

// fetchResult is one conditional GET's answer: either the feed was unchanged,
// or these are its vehicles and the validators to send next time.
type fetchResult struct {
	entities     map[TripKey]TrustedVehicle
	etag         string
	lastModified string
	notModified  bool
}

// fetch performs one conditional GET and decodes the vehicle positions in it.
func (f *TrustedFeed) fetch(ctx context.Context, url, etag, lastModified string) (fetchResult, error) {
	ctx, cancel := context.WithTimeout(ctx, trustedRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fetchResult{}, err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return fetchResult{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return fetchResult{notModified: true}, nil
	case http.StatusOK:
	default:
		return fetchResult{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, trustedMaxBodyBytes+1))
	if err != nil {
		return fetchResult{}, err
	}
	if len(body) > trustedMaxBodyBytes {
		return fetchResult{}, fmt.Errorf("feed body exceeds %d bytes", trustedMaxBodyBytes)
	}

	var msg gtfsrt.FeedMessage
	if err := proto.Unmarshal(body, &msg); err != nil {
		return fetchResult{}, fmt.Errorf("decode GTFS-RT: %w", err)
	}
	return fetchResult{
		entities:     vehiclesByTrip(&msg),
		etag:         resp.Header.Get("ETag"),
		lastModified: resp.Header.Get("Last-Modified"),
	}, nil
}

// vehiclesByTrip indexes the usable vehicle positions in a feed by trip.
// Entities without a trip id, without a position, or without a timestamp say
// nothing about where a trip is, so they are dropped.
func vehiclesByTrip(msg *gtfsrt.FeedMessage) map[TripKey]TrustedVehicle {
	out := make(map[TripKey]TrustedVehicle, len(msg.GetEntity()))
	for _, ent := range msg.GetEntity() {
		vp := ent.GetVehicle()
		if vp == nil || vp.GetPosition() == nil || vp.GetTimestamp() == 0 {
			continue
		}
		tripID := vp.GetTrip().GetTripId()
		if tripID == "" {
			continue
		}
		out[TripKey{TripID: tripID, StartDate: vp.GetTrip().GetStartDate()}] = TrustedVehicle{
			VehicleID: vp.GetVehicle().GetId(),
			Pos: LatLon{
				Lat: float64(vp.GetPosition().GetLatitude()),
				Lon: float64(vp.GetPosition().GetLongitude()),
			},
			Timestamp: time.Unix(int64(vp.GetTimestamp()), 0),
		}
	}
	return out
}

// Lookup returns the freshest agency position for key: an exact {trip,
// start_date} match wins over a feed that publishes the trip without a date.
// Positions older than maxAge are ignored. When several feeds carry the same
// key, the later-configured feed wins.
func (f *TrustedFeed) Lookup(key TripKey, now time.Time) (TrustedVehicle, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if v, ok := f.lookupLocked(key, now); ok {
		return v, true
	}
	if key.StartDate == "" {
		return TrustedVehicle{}, false
	}
	return f.lookupLocked(TripKey{TripID: key.TripID}, now)
}

// lookupLocked finds the last configured feed holding a fresh entity for key.
func (f *TrustedFeed) lookupLocked(key TripKey, now time.Time) (TrustedVehicle, bool) {
	var found TrustedVehicle
	var ok bool
	for _, feed := range f.feeds {
		v, present := feed.entities[key]
		if !present || now.Sub(v.Timestamp) > f.maxAge {
			continue
		}
		found, ok = v, true
	}
	return found, ok
}

// Covers reports whether a trusted position is available for key right now.
func (f *TrustedFeed) Covers(key TripKey, now time.Time) bool {
	_, ok := f.Lookup(key, now)
	return ok
}

// Health returns one entry per configured feed, in configuration order.
func (f *TrustedFeed) Health() []FeedHealth {
	f.mu.RLock()
	defer f.mu.RUnlock()

	out := make([]FeedHealth, 0, len(f.feeds))
	for _, feed := range f.feeds {
		out = append(out, FeedHealth{
			URL:         feed.url,
			LastSuccess: feed.lastSuccess,
			LastError:   feed.lastError,
			Entities:    len(feed.entities),
		})
	}
	return out
}
