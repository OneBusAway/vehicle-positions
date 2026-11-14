package rider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildIndex_Timepoints(t *testing.T) {
	timepoints := func(ix *Index, tripID string) []bool {
		trip, ok := ix.Trip(tripID)
		require.True(t, ok)
		flags := make([]bool, 0, len(trip.StopTimes))
		for _, st := range trip.StopTimes {
			flags = append(flags, st.Timepoint)
		}
		return flags
	}

	assert.Equal(t, []bool{true, true, true}, timepoints(fixtureIndex(t), "T1"),
		"a feed with no timepoint column has exact times at every stop")

	ix := fixtureIndexEdited(t, "stop_times.txt",
		"trip_id,arrival_time,departure_time,stop_id,stop_sequence,shape_dist_traveled,timepoint\n"+
			"T1,08:00:00,08:00:00,ST1,1,0,1\n"+
			"T1,08:05:00,08:05:00,ST2,2,500,0\n"+
			"T1,08:10:00,08:10:00,ST3,3,1001,\n")
	assert.Equal(t, []bool{true, false, false}, timepoints(ix, "T1"), "1 is a timepoint; 0 and an empty cell are not")
}
