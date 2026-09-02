package rider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// t1Input returns a VerifyInput for fixture trip T1 (08:00–08:10 on 20260902,
// straight 1 km north) at 08:02:30 local, positioned 250 m along the shape,
// which is exactly on schedule.
func t1Input(t *testing.T) VerifyInput {
	t.Helper()
	ix := fixtureIndex(t)
	trip, ok := ix.Trip("T1")
	require.True(t, ok)
	now := time.Date(2026, 9, 2, 8, 2, 30, 0, ix.Timezone())
	return VerifyInput{
		Trip: trip, Timezone: ix.Timezone(), StartDate: "20260902",
		Point:      Point{Pos: trip.Shape.PointAt(250), Accuracy: 8, Speed: 9, Bearing: 0, Timestamp: now},
		Thresholds: DefaultThresholds(), Now: now,
	}
}

func TestVerify_Matched(t *testing.T) {
	v := Verify(t1Input(t))
	assert.Equal(t, Matched, v.Outcome)
	assert.Equal(t, Unavailable, v.Corroboration)
	assert.InDelta(t, 250, v.AlongShape, 2)
	assert.InDelta(t, 0, v.DistanceToShape, 1)
	assert.InDelta(t, 0, float64(v.ScheduleDeviation), float64(5*time.Second))
}

func TestVerify_Ignored(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*VerifyInput)
	}{
		{"accuracy too coarse", func(in *VerifyInput) { in.Point.Accuracy = 150 }},
		{"too old", func(in *VerifyInput) { in.Point.Timestamp = in.Now.Add(-11 * time.Minute) }},
		{"too far in future", func(in *VerifyInput) { in.Point.Timestamp = in.Now.Add(31 * time.Second) }},
		{"null island", func(in *VerifyInput) { in.Point.Pos = LatLon{0, 0} }},
		{"latitude out of range", func(in *VerifyInput) { in.Point.Pos.Lat = 91 }},
		{"not newer than prev", func(in *VerifyInput) {
			in.Prev = &AcceptedPoint{Point: Point{Timestamp: in.Point.Timestamp}, AlongShape: 240}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := t1Input(t)
			tc.mut(&in)
			v := Verify(in)
			assert.Equal(t, Ignored, v.Outcome)
			assert.NotEmpty(t, v.Reason)
		})
	}
}

func TestVerify_OffRoute_AccuracyWidensTolerance(t *testing.T) {
	in := t1Input(t)
	// ~75 m east of the line.
	in.Point.Pos = LatLon{47.60225, -122.3290}
	in.Point.Accuracy = 5
	assert.Equal(t, OffRoute, Verify(in).Outcome, "75 m > 60 + 5")
	in.Point.Accuracy = 20
	assert.Equal(t, Matched, Verify(in).Outcome, "75 m <= 60 + 20")
	in.Point.Accuracy = -1
	assert.Equal(t, OffRoute, Verify(in).Outcome, "absent accuracy adds nothing")
}

func TestVerify_Implausible(t *testing.T) {
	in := t1Input(t)
	in.Prev = &AcceptedPoint{Point: Point{Timestamp: in.Now.Add(-5 * time.Second)}, AlongShape: 20}
	assert.Equal(t, Implausible, Verify(in).Outcome, "230 m in 5 s = 46 m/s")

	in.Prev = &AcceptedPoint{Point: Point{Timestamp: in.Now.Add(-30 * time.Second)}, AlongShape: 20}
	assert.Equal(t, Matched, Verify(in).Outcome, "230 m in 30 s = 7.7 m/s")

	in.Prev = &AcceptedPoint{Point: Point{Timestamp: in.Now.Add(-30 * time.Second)}, AlongShape: 320}
	v := Verify(in)
	assert.Equal(t, Implausible, v.Outcome, "regressed 70 m > 50 m")

	in.Prev = &AcceptedPoint{Point: Point{Timestamp: in.Now.Add(-30 * time.Second)}, AlongShape: 280}
	assert.Equal(t, Matched, Verify(in).Outcome, "30 m regression is GPS noise")
}

func TestVerify_ProjectionUsesPrevAsHint(t *testing.T) {
	ix := fixtureIndex(t)
	trip, _ := ix.Trip("T3")                                 // loop, 25:00–25:20 on the service day
	now := time.Date(2026, 9, 3, 1, 19, 0, 0, ix.Timezone()) // 25:19 of service day 20260902
	in := VerifyInput{
		Trip: trip, Timezone: ix.Timezone(), StartDate: "20260902",
		Prev:       &AcceptedPoint{Point: Point{Timestamp: now.Add(-10 * time.Second)}, AlongShape: trip.Shape.Length - 60},
		Point:      Point{Pos: LatLon{47.6001, -122.3299}, Accuracy: 5, Speed: 5, Bearing: 270, Timestamp: now},
		Thresholds: DefaultThresholds(), Now: now,
	}
	v := Verify(in)
	assert.Equal(t, Matched, v.Outcome)
	assert.Greater(t, v.AlongShape, trip.Shape.Length-100, "near the loop closure, the hint keeps us at the end")
}

func TestVerify_OffSchedule(t *testing.T) {
	in := t1Input(t)
	// 20 minutes early: 07:42:30 at 250 m along (scheduled 08:02:30).
	in.Now = in.Now.Add(-20 * time.Minute)
	in.Point.Timestamp = in.Now
	v := Verify(in)
	assert.Equal(t, OffSchedule, v.Outcome)
	assert.InDelta(t, float64(-20*time.Minute), float64(v.ScheduleDeviation), float64(5*time.Second))

	// 89 minutes late is still inside the window.
	in.Now = in.Now.Add(20*time.Minute + 89*time.Minute)
	in.Point.Timestamp = in.Now
	assert.Equal(t, Matched, Verify(in).Outcome)

	// 91 minutes late is not.
	in.Now = in.Now.Add(2 * time.Minute)
	in.Point.Timestamp = in.Now
	assert.Equal(t, OffSchedule, Verify(in).Outcome)
}

func TestVerify_OffSchedule_PostMidnightTrip(t *testing.T) {
	ix := fixtureIndex(t)
	trip, _ := ix.Trip("T3") // 25:00 = 01:00 next calendar day
	now := time.Date(2026, 9, 3, 1, 0, 30, 0, ix.Timezone())
	in := VerifyInput{
		Trip: trip, Timezone: ix.Timezone(), StartDate: "20260902",
		Point:      Point{Pos: trip.Shape.PointAt(5), Accuracy: 5, Speed: 1, Bearing: 0, Timestamp: now},
		Thresholds: DefaultThresholds(), Now: now,
	}
	v := Verify(in)
	assert.Equal(t, Matched, v.Outcome)
	assert.InDelta(t, float64(30*time.Second), float64(v.ScheduleDeviation), float64(5*time.Second))
}

func TestVerify_Corroboration(t *testing.T) {
	base := t1Input(t)
	trip := base.Trip
	cases := []struct {
		name    string
		trusted TrustedVehicle
		want    Corroboration
	}{
		{"within allowance", TrustedVehicle{"v1", trip.Shape.PointAt(300), base.Now}, Corroborated},
		{"age widens allowance", TrustedVehicle{"v1", trip.Shape.PointAt(700), base.Now.Add(-10 * time.Second)}, Corroborated}, // 450 m ≤ 150 + 35*10
		{"grey zone", TrustedVehicle{"v1", trip.Shape.PointAt(650), base.Now}, NoCorroboration},                                // 400 m: > 150, ≤ 650
		{"contradicted", TrustedVehicle{"v1", trip.Shape.PointAt(950), base.Now}, Contradicted},                                // 700 m > 650
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := withTrusted(base, tc.trusted)
			v := Verify(in)
			require.Equal(t, Matched, v.Outcome)
			assert.Equal(t, tc.want, v.Corroboration)
		})
	}

	t.Run("not computed for non-matched points", func(t *testing.T) {
		in := withTrusted(base, TrustedVehicle{"v1", trip.Shape.PointAt(250), base.Now})
		in.Point.Accuracy = 500
		v := Verify(in)
		assert.Equal(t, Ignored, v.Outcome)
		assert.Equal(t, Unavailable, v.Corroboration)
	})
}

// withTrusted attaches a trusted vehicle to an input the way ApplyBatch does:
// with its position already projected onto the trip's shape.
func withTrusted(in VerifyInput, tv TrustedVehicle) VerifyInput {
	along := in.Trip.Shape.Project(tv.Pos, nil).AlongShape
	in.Trusted, in.TrustedAlong = &tv, &along
	return in
}

func TestOutcomeAndCorroborationStrings(t *testing.T) {
	assert.Equal(t, "off_route", OffRoute.String())
	assert.Equal(t, "matched", Matched.String())
	assert.Equal(t, "none", NoCorroboration.String())
	assert.Equal(t, "contradicted", Contradicted.String())
}
