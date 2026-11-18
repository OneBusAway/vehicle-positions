package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBarChart(t *testing.T) {
	bars := []chartBar{
		{Label: "20:00", Value: 10},
		{Label: "20:01", Value: 0},
		{Label: "20:02", Value: 20, Warn: 5},
		{Label: "20:03", Value: 0, Warn: 2},
		{Label: "20:04", Value: 4},
	}
	c := newBarChart(bars, barChartOptions{Name: "Reports a minute", Height: 84, Unit: "accepted", WarnUnit: "rejected", Peak: "Most in a minute"})

	assert.Equal(t, "Reports a minute: 5 bars, the highest 25", c.Name)
	assert.Equal(t, "Most in a minute: 25", c.PeakLabel)
	assert.Equal(t, barChartWidth, c.Width)
	assert.Equal(t, 84, c.Height)
	require.Len(t, c.Bars, 5)

	// Each bar has a fifth of the width and takes the middle 56% of it.
	assert.Equal(t, "0.0", c.Bars[0].HitX)
	assert.Equal(t, "152.0", c.Bars[0].HitW)
	assert.Equal(t, "33.4", c.Bars[0].X)
	assert.Equal(t, "85.1", c.Bars[0].W)
	assert.Equal(t, "304.0", c.Bars[2].HitX)

	// 76 px of height for 112% of the tallest bar: a count of 10 is 27.1 px
	// up from the baseline at 82.
	assert.Equal(t, "27.1", c.Bars[0].H)
	assert.Equal(t, "54.9", c.Bars[0].Y)
	assert.Equal(t, "82.5", c.AxisY)
	assert.Equal(t, "14.1", c.GridY, "the line that marks the tallest bar")

	assert.Empty(t, c.Bars[1].H, "a minute with nothing has no bar")
	assert.Empty(t, c.Bars[1].WarnH)
	assert.Equal(t, "20:01|0 accepted", c.Bars[1].Tip)

	assert.Equal(t, "54.3", c.Bars[2].H)
	assert.Equal(t, "15.6", c.Bars[2].WarnH, "the stacked part, with its gap")
	assert.Equal(t, "10.1", c.Bars[2].WarnY)
	assert.Equal(t, "20:02|20 accepted, 5 rejected", c.Bars[2].Tip)

	assert.Empty(t, c.Bars[3].H)
	assert.NotEmpty(t, c.Bars[3].WarnH, "rejected reports alone still show")

	assert.Equal(t, []string{"20:00", "20:01", "20:02", "20:03", "20:04"}, c.Ticks)
}

func TestNewBarChart_Ticks(t *testing.T) {
	bars := make([]chartBar, 60)
	for i := range bars {
		bars[i].Label = string(rune('A' + i))
	}
	c := newBarChart(bars, barChartOptions{Name: "n", Height: 84})

	assert.Equal(t, []string{"A", string(rune('A' + 15)), string(rune('A' + 30)), string(rune('A' + 44)), string(rune('A' + 59))}, c.Ticks, "the first, the last and three between")
	assert.Empty(t, c.PeakLabel, "not asked for")
}

func TestNewBarChart_NothingToDraw(t *testing.T) {
	flat := newBarChart([]chartBar{{Label: "a"}, {Label: "b"}}, barChartOptions{Name: "n", Height: 84, Peak: "Most"})
	assert.Empty(t, flat.PeakLabel, "no tallest bar to name")
	for _, b := range flat.Bars {
		assert.Empty(t, b.H)
	}

	none := newBarChart(nil, barChartOptions{Name: "n", Height: 84})
	assert.Empty(t, none.Bars)
	assert.Empty(t, none.Ticks)
}

func ptrs(values ...float64) []*float64 {
	out := make([]*float64, 0, len(values))
	for _, v := range values {
		if v < 0 {
			out = append(out, nil)
			continue
		}
		out = append(out, &v)
	}
	return out
}

func TestNewLineChart(t *testing.T) {
	// -1 stands for a day with no figure.
	c := newLineChart([]string{"a", "b", "c", "d"}, ptrs(50, 100, -1, 0), ptrs(-1, 75, 75, -1), lineChartOptions{Name: "On time by day", Min: 0, Max: 100})

	assert.Equal(t, "On time by day", c.Name)
	assert.Equal(t, 1120, c.Width)
	assert.Equal(t, 300, c.Height)
	assert.Equal(t, "36.0", c.Left)
	assert.Equal(t, "1108.0", c.Right)
	assert.Equal(t, []chartGridLine{
		{Y: "274.0", LabelY: "278.0", Label: "0%"},
		{Y: "143.0", LabelY: "147.0", Label: "50%"},
		{Y: "12.0", LabelY: "16.0", Label: "100%"},
	}, c.Grid)
	assert.Equal(t, "M36.0 143.0L393.3 12.0M1108.0 274.0", c.Path, "a day with no figure breaks the line")
	assert.Equal(t, "M393.3 77.5L750.7 77.5", c.PrevPath)
	assert.Equal(t, []chartPoint{{X: "1108.0", Y: "274.0"}}, c.Dots, "a day with none either side has a dot, or it would not show")
	require.NotNil(t, c.Last)
	assert.Equal(t, chartPoint{X: "1108.0", Y: "274.0"}, *c.Last)
	assert.Equal(t, []chartAxisLabel{
		{X: "36.0", Anchor: "start", Text: "a"},
		{X: "393.3", Anchor: "middle", Text: "b"},
		{X: "750.7", Anchor: "middle", Text: "c"},
		{X: "1108.0", Anchor: "end", Text: "d"},
	}, c.Labels)
	assert.Equal(t, []chartHit{
		{X: "36.0", W: "178.7", Tip: "a|50% (before: no figure)"},
		{X: "214.7", W: "357.3", Tip: "b|100% (before: 75%)"},
		{X: "572.0", W: "357.3", Tip: "c|no figure (before: 75%)"},
		{X: "929.3", W: "178.7", Tip: "d|0% (before: no figure)"},
	}, c.Hits, "each day's strip stays inside the plot")
}

func TestNewLineChart_Edges(t *testing.T) {
	one := newLineChart([]string{"a"}, ptrs(80), nil, lineChartOptions{Name: "n", Min: 60, Max: 100})
	assert.Equal(t, []chartPoint{{X: "572.0", Y: "143.0"}}, one.Dots, "one day sits in the middle")
	assert.Equal(t, []chartAxisLabel{{X: "572.0", Anchor: "middle", Text: "a"}}, one.Labels)
	assert.Equal(t, []chartHit{{X: "36.0", W: "1072.0", Tip: "a|80%"}}, one.Hits)
	assert.Empty(t, one.PrevPath)
	assert.Equal(t, "80%", one.Grid[1].Label)

	none := newLineChart([]string{"a", "b"}, ptrs(-1, -1), nil, lineChartOptions{Name: "n", Min: 0, Max: 10})
	assert.Empty(t, none.Path)
	assert.Nil(t, none.Last)
	assert.Empty(t, none.Dots)

	labels := make([]string, 28)
	values := make([]float64, 28)
	for i := range labels {
		labels[i] = string(rune('A' + i))
	}
	long := newLineChart(labels, ptrs(values...), nil, lineChartOptions{Name: "n", Min: 0, Max: 10})
	assert.Len(t, long.Labels, 8, "every fourth day and the last")
	assert.Equal(t, labels[27], long.Labels[7].Text)
}

func TestNewHourChart(t *testing.T) {
	c := newHourChart([]hourShares{{Hour: 7, Trips: 2, reportCounts: reportCounts{OnTime: 8, Early: 1, Late: 1}}})

	assert.Equal(t, "Share of stops early or late, by hour of day", c.Name)
	assert.Equal(t, []chartGridLine{
		{Y: "115.3", LabelY: "119.3", Label: "10%"},
		{Y: "62.7", LabelY: "66.7", Label: "20%"},
		{Y: "10.0", LabelY: "14.0", Label: "30%"},
	}, c.Grid, "a fifth of the stops off time is drawn under a line at 30")
	assert.Equal(t, []hourChartBar{{
		X: "186.5", W: "220.9",
		LateY: "115.3", LateH: "52.7",
		EarlyY: "60.7", EarlyH: "52.7",
		HitX: "34.0", HitW: "526.0",
		Tip:   "07:00 to 08:00|10% late, 10% early, 80% on time (2 trips)",
		Label: "07:00", LabelX: "297.0",
	}}, c.Bars)
	assert.Equal(t, []hourChartRow{{Hour: "07:00", Trips: 2, OnTime: "80%", Early: "10%", Late: "10%"}}, c.Rows)
}

func TestNewHourChart_ScalesAndTips(t *testing.T) {
	top := func(hours ...hourShares) string {
		grid := newHourChart(hours).Grid
		return grid[len(grid)-1].Label
	}
	off := func(late int) hourShares {
		return hourShares{Trips: 1, reportCounts: reportCounts{OnTime: 100 - late, Late: late}}
	}
	assert.Equal(t, "15%", top(off(0)))
	assert.Equal(t, "15%", top(off(15)))
	assert.Equal(t, "30%", top(off(16)))
	assert.Equal(t, "60%", top(off(31)))
	assert.Equal(t, "100%", top(off(61), off(100)))
	assert.Len(t, newHourChart([]hourShares{off(100)}).Grid, 4)

	c := newHourChart([]hourShares{
		{Hour: 22, Trips: 3, reportCounts: reportCounts{Late: 1}},
		{Hour: 23},
		{Hour: 24, Trips: 1},
		{Hour: 25, Trips: 1, reportCounts: reportCounts{OnTime: 1}},
	})
	tips := make([]string, 0, 4)
	labels := make([]string, 0, 4)
	for _, b := range c.Bars {
		tips = append(tips, b.Tip)
		labels = append(labels, b.Label)
	}
	assert.Equal(t, []string{
		"22:00 to 23:00|100% late, 0% early, 0% on time (3 trips)",
		"23:00 to 00:00|no trip scheduled",
		"00:00 to 01:00|no stop judged (1 trip)",
		"01:00 to 02:00|0% late, 0% early, 100% on time (1 trip)",
	}, tips)
	assert.Equal(t, []string{"22:00", "", "", "01:00"}, labels, "every third hour has its label")
	assert.Empty(t, c.Bars[3].LateH)
	assert.Empty(t, c.Bars[3].EarlyH)
	assert.Equal(t, hourChartRow{Hour: "00:00", Trips: 1}, c.Rows[2])

	assert.Empty(t, newHourChart(nil).Bars)
}

func TestNewHistChart(t *testing.T) {
	buckets := make([]deviationBucket, 17)
	for i := range buckets {
		buckets[i].Minute = i - 4
	}
	buckets[0].Early = 2
	buckets[3].OnTime = 4
	buckets[9].OnTime, buckets[9].Late = 1, 1

	c := newHistChart(buckets, time.Minute, 5*time.Minute)

	assert.Equal(t, "Minutes early or late at the last stop", c.Name)
	assert.Equal(t, "168.0", c.AxisY)
	assert.Equal(t, "98.8", c.BandX, "the window starts a minute early, three bars in")
	assert.Equal(t, "197.6", c.BandW, "and is six minutes wide")
	assert.Equal(t, "164.0", c.BandH)
	assert.Equal(t, "197.6", c.BandLabelX)
	require.Len(t, c.Bars, 17)

	assert.Equal(t, histChartBar{X: "3.0", W: "26.9", EarlyY: "95.3", EarlyH: "72.7", HitX: "0.0", HitW: "32.9", Tip: "More than 3 min early|2 trips"}, c.Bars[0])
	assert.Equal(t, "22.5", c.Bars[3].OnTimeY, "the tallest bar stops short of the top")
	assert.Equal(t, "145.5", c.Bars[3].OnTimeH)
	assert.Equal(t, "Up to 1 min early|4 trips", c.Bars[3].Tip)
	// The minute the window ends in has a trip of each verdict, one on the
	// other.
	assert.Equal(t, "131.6", c.Bars[9].OnTimeY)
	assert.Equal(t, "36.4", c.Bars[9].OnTimeH)
	assert.Equal(t, "95.3", c.Bars[9].LateY)
	assert.Equal(t, "36.4", c.Bars[9].LateH)
	assert.Equal(t, "5 to 6 min late|2 trips", c.Bars[9].Tip)

	tips := map[int]string{1: "2 to 3 min early|0 trips", 4: "Under 1 min late|0 trips", 5: "1 to 2 min late|0 trips", 16: "12 min or more late|0 trips"}
	for i, want := range tips {
		assert.Equal(t, want, c.Bars[i].Tip)
	}
	labels := make(map[string]string)
	for _, b := range c.Bars {
		if b.Label != "" {
			labels[b.Label] = b.LabelX
		}
	}
	assert.Equal(t, map[string]string{"-3": "32.9", "0": "131.8", "+3": "230.6", "+6": "329.4", "+9": "428.2", "+12": "527.1"}, labels, "each at the left edge of its minute")
	assert.Equal(t, histChartRow{Minutes: "Up to 1 min early", Trips: 4}, c.Rows[3])

	assert.Empty(t, newHistChart(nil, time.Minute, 5*time.Minute).Bars)
}
