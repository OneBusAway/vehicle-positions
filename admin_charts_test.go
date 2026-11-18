package main

import (
	"testing"

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
