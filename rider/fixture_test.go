package rider

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureTimezone is the agency timezone of the test feed.
const fixtureTimezone = "America/Los_Angeles"

// fixtureLoadedAt is a fixed load time so fixture-derived stats are stable.
var fixtureLoadedAt = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

// buildFixtureZip returns the bytes of the committed test GTFS feed.
func buildFixtureZip(t *testing.T) []byte {
	t.Helper()
	return buildFixtureZipWith(t, fixtureTimezone, 1)
}

// buildFixtureZipWith renders the test GTFS feed with the given agency
// timezone and shape_dist_traveled multiplier (1 for metres, 0.001 for
// kilometres). The bytes are byte-identical across runs: zip entries are
// written in a fixed order with a zero modification time.
func buildFixtureZipWith(t *testing.T, timezone string, distScale float64) []byte {
	t.Helper()
	return zipFixtureFiles(t, fixtureFiles(timezone, distScale))
}

// zipFixtureFiles renders CSV members as a GTFS zip. Entries are written in
// the given order with a zero modification time, so the bytes are stable.
func zipFixtureFiles(t *testing.T, files []fixtureFile) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range files {
		entry, err := w.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate})
		require.NoError(t, err)
		_, err = entry.Write([]byte(f.body))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// fixtureIndexEdited builds an index over the test feed with one CSV member
// replaced. The committed fixture is left untouched, so cases that only one
// test needs do not have to live in it.
func fixtureIndexEdited(t *testing.T, name, body string) *Index {
	t.Helper()
	files := fixtureFiles(fixtureTimezone, 1)
	replaced := false
	for i := range files {
		if files[i].name == name {
			files[i].body = body
			replaced = true
		}
	}
	require.True(t, replaced, "no fixture member named %q", name)
	static, err := gtfs.ParseStatic(zipFixtureFiles(t, files), gtfs.ParseStaticOptions{})
	require.NoError(t, err)
	ix, err := BuildIndex(static, "fixture", fixtureLoadedAt)
	require.NoError(t, err)
	return ix
}

// fixtureStatic parses the test feed with the given agency timezone.
func fixtureStatic(t *testing.T, timezone string) *gtfs.Static {
	t.Helper()
	static, err := gtfs.ParseStatic(buildFixtureZipWith(t, timezone, 1), gtfs.ParseStaticOptions{})
	require.NoError(t, err)
	return static
}

// fixtureIndex builds an index over the test feed.
func fixtureIndex(t *testing.T) *Index {
	t.Helper()
	ix, err := BuildIndex(fixtureStatic(t, fixtureTimezone), "fixture", fixtureLoadedAt)
	require.NoError(t, err)
	return ix
}

// fixtureIndexWithShapeDistScale builds an index over the test feed whose
// shape_dist_traveled values are multiplied by scale.
func fixtureIndexWithShapeDistScale(t *testing.T, scale float64) *Index {
	t.Helper()
	static, err := gtfs.ParseStatic(buildFixtureZipWith(t, fixtureTimezone, scale), gtfs.ParseStaticOptions{})
	require.NoError(t, err)
	ix, err := BuildIndex(static, "fixture", fixtureLoadedAt)
	require.NoError(t, err)
	return ix
}

// fixtureFile is one CSV member of the test feed.
type fixtureFile struct{ name, body string }

// fixtureFiles returns the feed's CSV files in a fixed order.
//
// The feed has two routes: R1 runs the straight north line of straightShape()
// as shape S1 (with shape_dist_traveled), R2 runs loopShape() as shape S2
// (without shape_dist_traveled, so stops must be projected). T4 deliberately
// has no shape and must be excluded from the index.
func fixtureFiles(timezone string, distScale float64) []fixtureFile {
	dist := func(metres float64) string {
		return strconv.FormatFloat(metres*distScale, 'f', 6, 64)
	}

	return []fixtureFile{
		{"agency.txt", "agency_id,agency_name,agency_url,agency_timezone\n" +
			"A,Test,http://example.com," + timezone + "\n"},

		{"routes.txt", "route_id,agency_id,route_short_name,route_long_name,route_type\n" +
			"R1,A,1,Straight,3\n" +
			"R2,A,2,Loop,3\n"},

		{"calendar.txt", "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\n" +
			"WEEKDAY,1,1,1,1,1,0,0,20260101,20261231\n" +
			"SAT,0,0,0,0,0,1,0,20260101,20261231\n"},

		// 2026-09-07 is Labor Day: weekday service is removed and Saturday
		// service is added.
		{"calendar_dates.txt", "service_id,date,exception_type\n" +
			"WEEKDAY,20260907,2\n" +
			"SAT,20260907,1\n"},

		{"shapes.txt", "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence,shape_dist_traveled\n" +
			shapeRows("S1", straightShape().Points, []string{dist(0), dist(500), dist(1001)}) +
			shapeRows("S2", loopShape().Points, nil)},

		{"stops.txt", "stop_id,stop_name,stop_lat,stop_lon\n" +
			stopRow("ST1", 47.6000, -122.3300) +
			stopRow("ST2", 47.6045, -122.3300) +
			stopRow("ST3", 47.6090, -122.3300) +
			stopRow("LP1", 47.6000, -122.3300) +
			stopRow("LP2", 47.6045, -122.3234) +
			stopRow("LP3", 47.6000, -122.3234)},

		{"trips.txt", "route_id,service_id,trip_id,shape_id\n" +
			"R1,WEEKDAY,T1,S1\n" +
			"R1,SAT,T2,S1\n" +
			"R2,WEEKDAY,T3,S2\n" +
			"R1,WEEKDAY,T4,\n"},

		{"stop_times.txt", "trip_id,arrival_time,departure_time,stop_id,stop_sequence,shape_dist_traveled\n" +
			stopTimeRow("T1", "08:00:00", "ST1", 1, dist(0)) +
			stopTimeRow("T1", "08:05:00", "ST2", 2, dist(500)) +
			stopTimeRow("T1", "08:10:00", "ST3", 3, dist(1001)) +
			stopTimeRow("T2", "09:00:00", "ST1", 1, dist(0)) +
			stopTimeRow("T2", "09:05:00", "ST2", 2, dist(500)) +
			stopTimeRow("T2", "09:10:00", "ST3", 3, dist(1001)) +
			// After-midnight loop trip without shape_dist_traveled; the final
			// LP1 repeats the first stop and must project to the loop's end.
			stopTimeRow("T3", "25:00:00", "LP1", 1, "") +
			stopTimeRow("T3", "25:10:00", "LP2", 2, "") +
			stopTimeRow("T3", "25:15:00", "LP3", 3, "") +
			stopTimeRow("T3", "25:20:00", "LP1", 4, "") +
			stopTimeRow("T4", "10:00:00", "ST1", 1, "") +
			stopTimeRow("T4", "10:10:00", "ST3", 2, "")},
	}
}

func shapeRows(id string, points []LatLon, dists []string) string {
	var b strings.Builder
	for i, p := range points {
		d := ""
		if dists != nil {
			d = dists[i]
		}
		fmt.Fprintf(&b, "%s,%s,%s,%d,%s\n", id, coord(p.Lat), coord(p.Lon), i+1, d)
	}
	return b.String()
}

func stopRow(id string, lat, lon float64) string {
	return fmt.Sprintf("%s,Stop %s,%s,%s\n", id, id, coord(lat), coord(lon))
}

func stopTimeRow(tripID, hhmmss, stopID string, sequence int, dist string) string {
	return fmt.Sprintf("%s,%s,%s,%s,%d,%s\n", tripID, hhmmss, hhmmss, stopID, sequence, dist)
}

func coord(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }

// TestWriteFixture regenerates testdata/fixture.zip when WRITE_FIXTURE=1 and
// otherwise asserts the committed file still matches the builder above.
func TestWriteFixture(t *testing.T) {
	b := buildFixtureZip(t)
	path := filepath.Join("testdata", "fixture.zip")
	if os.Getenv("WRITE_FIXTURE") == "1" {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, b, 0o644))
		return
	}
	onDisk, err := os.ReadFile(path)
	require.NoError(t, err, "run WRITE_FIXTURE=1 go test ./rider -run TestWriteFixture")
	assert.Equal(t, b, onDisk, "committed fixture drifted; regenerate it")
}
