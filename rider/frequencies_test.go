package rider

import (
	"testing"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A trip's frequencies.txt rows come out by start_time, with exact_times
// read as a yes or no, and changing what comes out leaves the trip alone.
func TestTripInfo_Frequencies(t *testing.T) {
	ix := fixtureIndex(t)
	t1, ok := ix.Trip("T1")
	require.True(t, ok)
	assert.Empty(t, t1.Frequencies(), "a trip run by timetable")

	t1.frequencies = []gtfs.Frequency{
		{StartTime: clock(19, 0, 0), EndTime: clock(21, 0, 0), Headway: 15 * time.Minute, ExactTimes: gtfs.FrequencyBased},
		{StartTime: clock(6, 0, 0), EndTime: clock(8, 0, 0), Headway: 10 * time.Minute, ExactTimes: gtfs.ScheduleBased},
	}
	got := t1.Frequencies()
	assert.Equal(t, []Frequency{
		{StartTime: clock(6, 0, 0), EndTime: clock(8, 0, 0), Headway: 10 * time.Minute, ExactTimes: true},
		{StartTime: clock(19, 0, 0), EndTime: clock(21, 0, 0), Headway: 15 * time.Minute},
	}, got)

	got[0].Headway = time.Hour
	assert.Equal(t, 10*time.Minute, t1.Frequencies()[0].Headway)
	assert.Equal(t, clock(19, 0, 0), t1.frequencies[0].StartTime, "the trip's own rows keep the feed's order")
}
