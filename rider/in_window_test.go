package rider

import (
	"testing"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndex_InWindow(t *testing.T) {
	ix := fixtureIndex(t)
	t1, ok := ix.Trip("T1") // weekdays
	require.True(t, ok)
	at := func(day, h, m int) time.Time { return time.Date(2026, 9, day, h, m, 0, 0, ix.Timezone()) }

	tests := []struct {
		name    string
		windows []gtfs.Frequency
		at      time.Time
		want    bool
	}{
		{"in the day's window", []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(24, 0, 0)}}, at(15, 8, 3), true},
		{"after midnight in the day before's window", []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(26, 0, 0)}}, at(15, 0, 30), true},
		{"before the day's window opens", []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(24, 0, 0)}}, at(15, 5, 59), false},
		{"at the window's start", []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(24, 0, 0)}}, at(15, 6, 0), true},
		{"at the window's end", []gtfs.Frequency{{StartTime: clock(6, 0, 0), EndTime: clock(9, 0, 0)}}, at(15, 9, 0), false},
		{"in the window of a day the trip does not run", []gtfs.Frequency{{StartTime: clock(0, 0, 0), EndTime: clock(24, 0, 0)}}, at(13, 23, 40), false},
		{"timetabled", nil, at(15, 8, 3), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t1.frequencies = tc.windows
			assert.Equal(t, tc.want, ix.InWindow(t1, tc.at))
		})
	}
}
