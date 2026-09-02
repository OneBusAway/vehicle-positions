package rider

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
)

const (
	// fetchTimeout bounds a single static feed download.
	fetchTimeout = 10 * time.Minute
	// maxFeedBytes caps how much of a downloaded feed is read.
	maxFeedBytes = 512 << 20
)

// LoadStatic reads and parses a GTFS static feed. The source is an http(s) URL
// or a local file path.
func LoadStatic(ctx context.Context, source string, client *http.Client) (*gtfs.Static, error) {
	body, err := fetchFeed(ctx, source, client)
	if err != nil {
		return nil, err
	}
	static, err := gtfs.ParseStatic(body, gtfs.ParseStaticOptions{})
	if err != nil {
		return nil, fmt.Errorf("rider: parse GTFS from %q: %w", source, err)
	}
	return static, nil
}

// LoadIndex loads a GTFS static feed and indexes it.
func LoadIndex(ctx context.Context, source string, client *http.Client, now time.Time) (*Index, error) {
	static, err := LoadStatic(ctx, source, client)
	if err != nil {
		return nil, err
	}
	return BuildIndex(static, source, now)
}

// fetchFeed returns the raw bytes of the feed at source.
func fetchFeed(ctx context.Context, source string, client *http.Client) ([]byte, error) {
	if !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
		body, err := os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf("rider: read GTFS file: %w", err)
		}
		return body, nil
	}

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, fmt.Errorf("rider: build GTFS request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rider: fetch GTFS from %q: %w", source, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rider: fetch GTFS from %q: status %d", source, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes))
	if err != nil {
		return nil, fmt.Errorf("rider: read GTFS from %q: %w", source, err)
	}
	return body, nil
}

// Refresher holds the current Index and swaps in reloaded ones. A failed reload
// leaves the previous index in place, and readers keep using whichever index
// they loaded, so trips already in progress stay valid.
type Refresher struct {
	current atomic.Pointer[Index]
	load    func(ctx context.Context) (*Index, error)
}

// NewRefresher returns a Refresher serving `initial` until the first successful
// reload.
func NewRefresher(initial *Index, load func(ctx context.Context) (*Index, error)) *Refresher {
	r := &Refresher{load: load}
	r.current.Store(initial)
	return r
}

// Current returns the index in force right now.
func (r *Refresher) Current() *Index { return r.current.Load() }

// RefreshNow reloads the index, swapping it in only on success.
func (r *Refresher) RefreshNow(ctx context.Context) error {
	next, err := r.load(ctx)
	if err != nil {
		return err
	}
	r.current.Store(next)
	return nil
}

// Start reloads the index every `every` until ctx is done. It blocks, so
// callers run it in a goroutine.
func (r *Refresher) Start(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.RefreshNow(ctx); err != nil {
				slog.Warn("rider: GTFS refresh failed, keeping the previous index", "error", err)
			}
		}
	}
}

// ParseStaticBytes parses an in-memory GTFS static feed. It exists so callers
// outside this package — tests, mainly — need not import go-gtfs themselves.
func ParseStaticBytes(b []byte) (*gtfs.Static, error) {
	return gtfs.ParseStatic(b, gtfs.ParseStaticOptions{})
}
