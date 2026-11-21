package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// chartBar is one bar of a bar chart: a count, a second count stacked on it
// in amber, and the label its value is read out under. Tip, when set, is what
// its tip reads after the label in place of the counts and their units.
type chartBar struct {
	Label string
	Value uint64
	Warn  uint64
	Tip   string
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
		if b.Tip != "" {
			bar.Tip = b.Label + "|" + b.Tip
		}
		view.Bars = append(view.Bars, bar)
	}
	for _, f := range []float64{0, 0.25, 0.5, 0.75, 1} {
		view.Ticks = append(view.Ticks, bars[int(f*float64(len(bars)-1)+0.5)].Label)
	}
	return view
}

// chartGridLine is a horizontal line of a chart with the value it marks.
type chartGridLine struct {
	Y, LabelY string
	Label     string
}

// chartPoint is a point of a chart, placed.
type chartPoint struct {
	X, Y string
}

// chartAxisLabel is a label under a chart: Anchor is how the text sits on X,
// "start", "middle" or "end".
type chartAxisLabel struct {
	X, Anchor, Text string
}

// chartHit is the strip of a chart that shows a tip, "label|value".
type chartHit struct {
	X, W string
	Tip  string
}

// lineChartOptions says what a trend shows. The line is drawn between Min
// and Max, which are percentages.
type lineChartOptions struct {
	Name string
	Min  int
	Max  int
}

// The size every trend is drawn at; the page scales the drawing to its card.
const (
	lineChartWidth  = 1120
	lineChartHeight = 300
)

// lineChartView is a trend as the "line-chart" template draws it. A day with
// no figure breaks the line; a day with a figure and none either side is in
// Dots, as a line through one point draws nothing.
type lineChartView struct {
	Name   string
	Width  int
	Height int
	// Left and Right are the ends of the plot; LabelX is where the values up
	// the side end.
	Left, Right, LabelX string
	PlotY, PlotH        string
	BaseY               string
	Grid                []chartGridLine
	Path                string
	// PrevPath is the period before, drawn dashed; "" when it is not shown.
	PrevPath string
	Dots     []chartPoint
	// Last is the newest day with a figure; nil when no day has one.
	Last   *chartPoint
	Labels []chartAxisLabel
	Hits   []chartHit
}

// newLineChart draws series, one percentage a day and nil for a day with
// none, over prev, the same days of the period before, which may be nil.
func newLineChart(labels []string, series, prev []*float64, o lineChartOptions) lineChartView {
	const padLeft, padRight, padTop, padBottom = 36.0, 12.0, 12.0, 26.0
	px := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
	plotW := lineChartWidth - padLeft - padRight
	plotH := lineChartHeight - padTop - padBottom

	step := plotW
	if len(series) > 1 {
		step = plotW / float64(len(series)-1)
	}
	x := func(i int) float64 {
		if len(series) < 2 {
			return padLeft + plotW/2
		}
		return padLeft + float64(i)*step
	}
	y := func(v float64) float64 {
		return padTop + (1-(v-float64(o.Min))/float64(o.Max-o.Min))*plotH
	}
	path := func(values []*float64) string {
		var b strings.Builder
		for i, v := range values {
			if v == nil {
				continue
			}
			cmd := "M"
			if i > 0 && values[i-1] != nil {
				cmd = "L"
			}
			b.WriteString(cmd + px(x(i)) + " " + px(y(*v)))
		}
		return b.String()
	}

	view := lineChartView{
		Name:     o.Name,
		Width:    lineChartWidth,
		Height:   lineChartHeight,
		Left:     px(padLeft),
		Right:    px(lineChartWidth - padRight),
		LabelX:   px(padLeft - 8),
		PlotY:    px(padTop),
		PlotH:    px(plotH),
		BaseY:    px(float64(lineChartHeight) - 6),
		Path:     path(series),
		PrevPath: path(prev),
		Dots:     make([]chartPoint, 0),
		Labels:   make([]chartAxisLabel, 0, len(labels)),
		Hits:     make([]chartHit, 0, len(series)),
	}
	for _, tick := range []int{o.Min, (o.Min + o.Max) / 2, o.Max} {
		at := y(float64(tick))
		view.Grid = append(view.Grid, chartGridLine{Y: px(at), LabelY: px(at + 4), Label: strconv.Itoa(tick) + "%"})
	}
	// About seven labels fit under the drawing, and the last day always has
	// one.
	every := (len(labels) + 6) / 7
	for i, v := range series {
		known := func(j int) bool { return j >= 0 && j < len(series) && series[j] != nil }
		if v != nil {
			view.Last = &chartPoint{X: px(x(i)), Y: px(y(*v))}
			if !known(i-1) && !known(i+1) {
				view.Dots = append(view.Dots, *view.Last)
			}
		}
		if i%every == 0 || i == len(labels)-1 {
			anchor := "middle"
			switch {
			case len(labels) == 1:
			case i == 0:
				anchor = "start"
			case i == len(labels)-1:
				anchor = "end"
			}
			view.Labels = append(view.Labels, chartAxisLabel{X: px(x(i)), Anchor: anchor, Text: labels[i]})
		}
		tip := labels[i] + "|" + percentTip(v)
		if i < len(prev) {
			tip += " (before: " + percentTip(prev[i]) + ")"
		}
		// A day's strip runs halfway to the days either side, and no further
		// than the plot.
		left, right := max(padLeft, x(i)-step/2), min(lineChartWidth-padRight, x(i)+step/2)
		view.Hits = append(view.Hits, chartHit{X: px(left), W: px(right - left), Tip: tip})
	}
	return view
}

// percentTip is a percentage as a chart's tip and table show it.
func percentTip(v *float64) string {
	if v == nil {
		return "no figure"
	}
	return strconv.Itoa(int(math.Round(*v))) + "%"
}

// hourSplit is an hour of the day on the agency's clock as an hour chart
// draws it: Count is what the hour holds, and Lower, Upper and Rest what was
// judged in it, by the verdicts its hourSeries names.
type hourSplit struct {
	Hour  int
	Count int
	Lower int
	Upper int
	Rest  int
}

func (h hourSplit) judged() int { return h.Lower + h.Upper + h.Rest }

// hourSeries names what an hour chart counts and the verdicts it splits
// them by, for its tips. Lower is drawn in late's blue at the bottom of each
// bar and Upper in early's red stacked on it; Rest is the space above, which
// a wall of green bars would only hide.
type hourSeries struct {
	// Name is the chart's name for a screen reader.
	Name string
	// Unit and Units are what an hour's Count counts, one and more than one.
	Unit, Units        string
	Lower, Upper, Rest string
	// Empty is the tip of an hour whose Count is zero, and NoneJudged the
	// tip of one with nothing judged; a series that counts only what was
	// judged needs none.
	Empty, NoneJudged string
}

// tripHours is "Early and late by hour of day": for the trips scheduled to
// leave in each hour, the share of their stops that were late, with the
// share that were early stacked on it.
var tripHours = hourSeries{
	Name: "Share of stops early or late, by hour of day",
	Unit: "trip", Units: "trips",
	Lower: "late", Upper: "early", Rest: "on time",
	Empty: "no trip scheduled", NoneJudged: "no stop judged",
}

// gapHours is "Bunched and long gaps by hour of day": of the gaps judged
// whose later vehicle left in each hour, the share that were long, with the
// share that were bunched stacked on it, in late's place and early's.
var gapHours = hourSeries{
	Name: "Share of gaps bunched or long, by hour of day",
	Unit: "gap", Units: "gaps",
	Lower: "long", Upper: "bunched", Rest: "neither",
	Empty: "no gap judged",
}

// hourChartView is an hour chart, "Early and late by hour of day" or
// "Bunched and long gaps by hour of day", as the "hour-chart" template draws
// it.
type hourChartView struct {
	Name   string
	Width  int
	Height int
	Left   string
	LabelX string
	AxisY  string
	Grid   []chartGridLine
	BaseY  string
	Bars   []hourChartBar
	Rows   []hourChartRow
}

// hourChartBar is one hour: its two rectangles, Lower's and Upper's stacked
// on it (a height is empty when the share is zero), the strip that shows its
// tip, and its label when it has one, which every third hour does.
type hourChartBar struct {
	X, W           string
	LowerY, LowerH string
	UpperY, UpperH string
	HitX, HitW     string
	Tip            string
	Label, LabelX  string
}

// hourChartRow is an hour in the table behind the chart. The shares are ""
// when nothing in the hour was judged.
type hourChartRow struct {
	Hour               string
	Count              int
	Lower, Upper, Rest string
}

// hourChartScales are the heights an hour chart is drawn to, each with the
// values its lines mark; the first that holds the tallest bar is used.
var hourChartScales = [][]int{{5, 10, 15}, {10, 20, 30}, {20, 40, 60}, {25, 50, 75, 100}}

// newHourChart draws hours, which run from the first hour with something to
// count to the last with none left out, as s names them.
func newHourChart(hours []hourSplit, s hourSeries) hourChartView {
	const width, height, padLeft, padBottom, padTop = 560.0, 190.0, 34.0, 22.0, 10.0
	px := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
	share := func(n, of int) float64 {
		if of == 0 {
			return 0
		}
		return float64(n) * 100 / float64(of)
	}

	peak := 0.0
	for _, h := range hours {
		peak = max(peak, share(h.Lower+h.Upper, h.judged()))
	}
	scale := hourChartScales[len(hourChartScales)-1]
	for _, sc := range hourChartScales {
		if float64(sc[len(sc)-1]) >= peak {
			scale = sc
			break
		}
	}
	bottom := height - padBottom
	y := func(p float64) float64 { return p / float64(scale[len(scale)-1]) * (bottom - padTop) }

	view := hourChartView{
		Name:   s.Name,
		Width:  int(width),
		Height: int(height),
		Left:   px(padLeft),
		LabelX: px(padLeft - 6),
		AxisY:  px(bottom),
		BaseY:  px(height - 6),
		Bars:   make([]hourChartBar, 0, len(hours)),
		Rows:   make([]hourChartRow, 0, len(hours)),
	}
	for _, tick := range scale {
		at := bottom - y(float64(tick))
		view.Grid = append(view.Grid, chartGridLine{Y: px(at), LabelY: px(at + 4), Label: strconv.Itoa(tick) + "%"})
	}
	if len(hours) == 0 {
		return view
	}
	slot := (width - padLeft) / float64(len(hours))
	for i, h := range hours {
		left := padLeft + float64(i)*slot
		lower, upper := share(h.Lower, h.judged()), share(h.Upper, h.judged())
		// Each share is rounded on its own, so each is the number the table
		// shows for it; the three can add up to 99 or 101.
		whole := func(n int) string { return strconv.Itoa(int(math.Round(share(n, h.judged())))) + "%" }
		from := fmt.Sprintf("%02d:00", h.Hour%24)
		bar := hourChartBar{
			X:    px(left + slot*0.29),
			W:    px(slot * 0.42),
			HitX: px(left),
			HitW: px(slot),
			Tip:  fmt.Sprintf("%s to %02d:00|", from, (h.Hour+1)%24),
		}
		row := hourChartRow{Hour: from, Count: h.Count}
		switch {
		case h.Count == 0:
			bar.Tip += s.Empty
		case h.judged() == 0:
			bar.Tip += fmt.Sprintf("%s (%s)", s.NoneJudged, countOf(h.Count, s.Unit, s.Units))
		default:
			row.Lower, row.Upper, row.Rest = whole(h.Lower), whole(h.Upper), whole(h.Rest)
			bar.Tip += fmt.Sprintf("%s %s, %s %s, %s %s (%s)", row.Lower, s.Lower, row.Upper, s.Upper, row.Rest, s.Rest, countOf(h.Count, s.Unit, s.Units))
		}
		if lower > 0 {
			bar.LowerY, bar.LowerH = px(bottom-y(lower)), px(y(lower))
		}
		if upper > 0 {
			// Two units of gap between the lower share and the upper one on it.
			bar.UpperY, bar.UpperH = px(bottom-y(lower)-2-y(upper)), px(y(upper))
		}
		if i%3 == 0 {
			bar.Label, bar.LabelX = from, px(left+slot/2)
		}
		view.Bars = append(view.Bars, bar)
		view.Rows = append(view.Rows, row)
	}
	return view
}

// deviationBucket is the trips that reached their last stop within a minute
// of each other against the timetable: Minute 2 is from two to under three
// minutes late, Minute -1 up to a minute early. Each trip is counted under
// the verdict it was given, so a bucket the on-time window ends inside has
// both.
type deviationBucket struct {
	Minute              int
	OnTime, Early, Late int
}

func (b deviationBucket) trips() int { return b.OnTime + b.Early + b.Late }

// histChartView is "How early and how late": a bar a minute, with the
// on-time window shaded behind them.
type histChartView struct {
	Name   string
	Width  int
	Height int
	AxisY  string
	BaseY  string
	// The band is the on-time window.
	BandX, BandW, BandH, BandLabelX string
	Bars                            []histChartBar
	Rows                            []histChartRow
}

// histChartBar is one minute: up to three rectangles stacked, on time first
// (a height is empty when there is none), the strip that shows its tip, and
// the label at its left edge when it has one.
type histChartBar struct {
	X, W             string
	OnTimeY, OnTimeH string
	EarlyY, EarlyH   string
	LateY, LateH     string
	HitX, HitW       string
	Tip              string
	Label, LabelX    string
}

// histChartRow is a minute in the table behind the chart.
type histChartRow struct {
	Minutes string
	Trips   int
}

// newHistChart draws buckets, which are a minute apart with none left out;
// the first also holds every trip earlier than it and the last every trip
// later. onTimeEarly and onTimeLate are the ends of the on-time window.
func newHistChart(buckets []deviationBucket, onTimeEarly, onTimeLate time.Duration) histChartView {
	const width, height, padBottom, padTop = 560.0, 190.0, 22.0, 8.0
	px := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
	bottom := height - padBottom

	view := histChartView{
		Name:   "Minutes early or late at the last stop",
		Width:  int(width),
		Height: int(height),
		AxisY:  px(bottom),
		BaseY:  px(height - 6),
		Bars:   make([]histChartBar, 0, len(buckets)),
		Rows:   make([]histChartRow, 0, len(buckets)),
	}
	if len(buckets) == 0 {
		return view
	}
	peak := 0
	for _, b := range buckets {
		peak = max(peak, b.trips())
	}
	y := func(n int) float64 {
		if peak == 0 {
			return 0
		}
		return float64(n) / (float64(peak) * 1.1) * (bottom - padTop)
	}
	slot := width / float64(len(buckets))
	first := buckets[0].Minute
	// Where a deviation of d sits along the bottom, kept inside the drawing.
	at := func(d time.Duration) float64 {
		return min(width, max(0, (d.Minutes()-float64(first))*slot))
	}
	bandLeft, bandRight := at(-onTimeEarly), at(onTimeLate)
	view.BandX, view.BandW, view.BandH = px(bandLeft), px(bandRight-bandLeft), px(bottom-4)
	view.BandLabelX = px((bandLeft + bandRight) / 2)

	for i, b := range buckets {
		left := float64(i) * slot
		bar := histChartBar{X: px(left + 3), W: px(slot - 6), HitX: px(left), HitW: px(slot)}
		top := bottom
		place := func(n int) (string, string) {
			if n == 0 {
				return "", ""
			}
			top -= y(n)
			return px(top), px(y(n))
		}
		bar.OnTimeY, bar.OnTimeH = place(b.OnTime)
		bar.EarlyY, bar.EarlyH = place(b.Early)
		bar.LateY, bar.LateH = place(b.Late)

		var minutes string
		switch {
		case i == 0 && b.Minute < -1:
			minutes = fmt.Sprintf("More than %d min early", -b.Minute-1)
		case i == len(buckets)-1 && b.Minute > 0:
			minutes = fmt.Sprintf("%d min or more late", b.Minute)
		case b.Minute == -1:
			minutes = "Up to 1 min early"
		case b.Minute == 0:
			minutes = "Under 1 min late"
		case b.Minute < 0:
			minutes = fmt.Sprintf("%d to %d min early", -b.Minute-1, -b.Minute)
		default:
			minutes = fmt.Sprintf("%d to %d min late", b.Minute, b.Minute+1)
		}
		bar.Tip = minutes + "|" + countOf(b.trips(), "trip", "trips")
		if b.Minute%3 == 0 && i > 0 {
			bar.Label, bar.LabelX = strconv.Itoa(b.Minute), px(left)
			if b.Minute > 0 {
				bar.Label = "+" + bar.Label
			}
		}
		view.Bars = append(view.Bars, bar)
		view.Rows = append(view.Rows, histChartRow{Minutes: minutes, Trips: b.trips()})
	}
	return view
}

// ratioChartView is "How even the gaps were": a bar for each tenth of the
// scheduled gap, with the bunched and long bands shaded behind them as the
// on-time window is on "How early and how late". A bar is not split by
// verdict, since the tenths where a band ends hold gaps of both.
type ratioChartView struct {
	Name   string
	Width  int
	Height int
	AxisY  string
	BaseY  string
	// The bunched band runs from the left edge to BunchedW, and the long
	// band from LongX to the right edge, LongW wide.
	BunchedW, BunchedLabelX  string
	LongX, LongW, LongLabelX string
	BandH                    string
	Bars                     []ratioChartBar
	Rows                     []ratioChartRow
}

// ratioChartBar is one tenth: its rectangle (H is empty when it has no
// gap), the strip that shows its tip, and the label at its left edge when it
// has one, which every fifth does.
type ratioChartBar struct {
	X, Y, W, H    string
	HitX, HitW    string
	Tip           string
	Label, LabelX string
}

// ratioChartRow is a tenth in the table behind the chart.
type ratioChartRow struct {
	Share string
	Gaps  int
}

// newRatioChart draws tenths, the gaps judged in each tenth of their
// scheduled gap from the first, under 10%, to gapRatioTop, which holds 300%
// and over.
func newRatioChart(tenths []int) ratioChartView {
	const width, height, padBottom, padTop = 560.0, 190.0, 22.0, 8.0
	px := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
	bottom := height - padBottom

	view := ratioChartView{
		Name:   "Gaps judged by their share of the scheduled gap",
		Width:  int(width),
		Height: int(height),
		AxisY:  px(bottom),
		BaseY:  px(height - 6),
		BandH:  px(bottom - 4),
		Bars:   make([]ratioChartBar, 0, len(tenths)),
		Rows:   make([]ratioChartRow, 0, len(tenths)),
	}
	if len(tenths) == 0 {
		return view
	}
	// Only a tenth with a gap is drawn, so the tallest has one.
	peak := slices.Max(tenths)
	y := func(n int) float64 { return float64(n) / (float64(peak) * 1.1) * (bottom - padTop) }
	slot := width / float64(len(tenths))
	// Where a share of the scheduled gap sits along the bottom.
	at := func(ratio float64) float64 { return ratio * 10 * slot }
	view.BunchedW, view.BunchedLabelX = px(at(bunchedBelow)), px(at(bunchedBelow)/2)
	view.LongX, view.LongW, view.LongLabelX = px(at(longGapAbove)), px(width-at(longGapAbove)), px((at(longGapAbove)+width)/2)

	for i, n := range tenths {
		left := float64(i) * slot
		share := fmt.Sprintf("%d%% to %d%%", 10*i, 10*(i+1))
		if i == len(tenths)-1 {
			share = fmt.Sprintf("%d%% or more", 10*i)
		}
		bar := ratioChartBar{
			X: px(left + 2), W: px(slot - 4), HitX: px(left), HitW: px(slot),
			Tip: share + " of the scheduled gap|" + countOf(n, "gap", "gaps"),
		}
		if n > 0 {
			bar.Y, bar.H = px(bottom-y(n)), px(y(n))
		}
		if i%5 == 0 && i > 0 {
			bar.Label, bar.LabelX = strconv.Itoa(10*i)+"%", px(left)
		}
		view.Bars = append(view.Bars, bar)
		view.Rows = append(view.Rows, ratioChartRow{Share: share, Gaps: n})
	}
	return view
}
