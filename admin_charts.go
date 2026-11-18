package main

import (
	"fmt"
	"strconv"
)

// chartBar is one bar of a bar chart: a count, a second count stacked on it
// in amber, and the label its value is read out under.
type chartBar struct {
	Label string
	Value uint64
	Warn  uint64
}

// barChartOptions says what a bar chart shows. Unit and WarnUnit follow a
// bar's two counts in its tip ("28 accepted, 1 rejected"); Peak, when set,
// names the tallest bar in a line over the chart ("Most in a minute").
type barChartOptions struct {
	Name     string
	Height   int
	Unit     string
	WarnUnit string
	Peak     string
}

// barChartWidth is the width every bar chart is drawn at; the page stretches
// the drawing to its card.
const barChartWidth = 760

// barChartView is a bar chart as the "bar-chart" template draws it: every
// number already placed, so the template only writes them out. The numbers
// reach the page through html/template like any other value.
type barChartView struct {
	Name   string
	Width  int
	Height int
	// PeakLabel is the line over the chart, empty when Peak was not asked
	// for or every bar is zero.
	PeakLabel string
	GridY     string
	AxisY     string
	Bars      []barChartBar
	// Ticks are the labels under the chart: the first bar's, the last's and
	// three between.
	Ticks []string
}

// barChartBar is one bar: its rectangle (H is empty for a count of zero,
// which has none), the amber one stacked on it (WarnH is empty when there is
// none), the full-height strip that shows its tip, and the tip, "label|value".
type barChartBar struct {
	X, Y, W, H string
	WarnY      string
	WarnH      string
	HitX, HitW string
	Tip        string
}

func newBarChart(bars []chartBar, o barChartOptions) barChartView {
	const padTop, padBottom = 6.0, 2.0
	px := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

	var peak uint64
	for _, b := range bars {
		peak = max(peak, b.Value+b.Warn)
	}
	// The tallest bar stops short of the top, so the line that marks it is
	// inside the drawing.
	scale := float64(o.Height) - padTop - padBottom
	y := func(v uint64) float64 {
		if peak == 0 {
			return 0
		}
		return float64(v) / (float64(peak) * 1.12) * scale
	}
	bottom := float64(o.Height) - padBottom

	view := barChartView{
		Name:   fmt.Sprintf("%s: %d bars, the highest %d", o.Name, len(bars), peak),
		Width:  barChartWidth,
		Height: o.Height,
		GridY:  px(bottom - y(peak)),
		AxisY:  px(bottom + 0.5),
		Bars:   make([]barChartBar, 0, len(bars)),
		Ticks:  make([]string, 0, 5),
	}
	if o.Peak != "" && peak > 0 {
		view.PeakLabel = fmt.Sprintf("%s: %d", o.Peak, peak)
	}
	if len(bars) == 0 {
		return view
	}
	slot := float64(barChartWidth) / float64(len(bars))
	for i, b := range bars {
		bar := barChartBar{
			X:    px(float64(i)*slot + slot*0.22),
			Y:    px(bottom - y(b.Value)),
			W:    px(slot * 0.56),
			HitX: px(float64(i) * slot),
			HitW: px(slot),
			Tip:  fmt.Sprintf("%s|%d %s", b.Label, b.Value, o.Unit),
		}
		if b.Value > 0 {
			bar.H = px(y(b.Value))
		}
		if b.Warn > 0 {
			// Two units of gap between the bar and what is stacked on it.
			bar.WarnH = px(y(b.Warn) + 2)
			bar.WarnY = px(bottom - y(b.Value) - y(b.Warn) - 4)
			bar.Tip += fmt.Sprintf(", %d %s", b.Warn, o.WarnUnit)
		}
		view.Bars = append(view.Bars, bar)
	}
	for _, f := range []float64{0, 0.25, 0.5, 0.75, 1} {
		view.Ticks = append(view.Ticks, bars[int(f*float64(len(bars)-1)+0.5)].Label)
	}
	return view
}
