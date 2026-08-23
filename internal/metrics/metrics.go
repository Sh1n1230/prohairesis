// Package metrics computes the friction figures this project is judged by.
//
// Two rules shape everything here.
//
// A figure that has not been measured is not zero. An indicator with no value
// carries the reason it has none, and prints as "n/a" with that reason attached.
// Printing 0 for "nothing is deciding yet, so nothing was wrongly refused" would
// be the same species of untruth this project refuses everywhere else, told with
// a number instead of a sentence.
//
// Both sides of a comparison come from the same code. The pre-install baseline
// and the current figure are computed by the functions below, over the same
// inputs, split by time -- never one by one implementation and one by another.
// Otherwise the difference between two implementations is indistinguishable from
// the effect being measured.
package metrics

import (
	"sort"
	"time"
)

// Sample is one agent session as an adapter observed it.
//
// It is deliberately not the adapter's own type: adapters depend on this
// package, never the other way round, so that nothing here knows which runtime
// produced the numbers.
type Sample struct {
	Source    string // where it was read from
	SessionID string
	CWD       string

	HumanTurns     int
	ToolCalls      int
	ToolErrors     int
	RepeatErrors   int
	UserRejections int

	First time.Time

	// UninterruptedS are the gaps, in seconds, between consecutive human turns.
	UninterruptedS []float64
}

// Indicator is one figure, with everything a reader needs to know whether to
// believe it.
type Indicator struct {
	Name     string
	Value    *float64
	Baseline *float64
	Unit     string
	// Direction is the way improvement points: "up" or "down".
	Direction string
	// Proxy says what was actually counted, when that differs from what the
	// name suggests. Every figure here is a proxy for something not directly
	// observable, and pretending otherwise is how a metric starts being
	// optimized instead of read.
	Proxy string
	// Unavailable is why there is no value. Set exactly when Value is nil.
	Unavailable string
}

func f(v float64) *float64 { return &v }

// Split divides samples into those from before the first observation this
// project recorded and those from after it.
//
// The boundary is derived from the event log itself rather than stored
// anywhere: the earliest event is, by definition, the moment this project
// started watching. A recorded install date would be a second copy of that fact,
// free to disagree with it.
func Split(samples []Sample, instrumentedAt time.Time) (before, after []Sample) {
	for _, s := range samples {
		if instrumentedAt.IsZero() || s.First.Before(instrumentedAt) {
			before = append(before, s)
			continue
		}
		after = append(after, s)
	}
	return before, after
}

type totals struct {
	sessions       int
	humanTurns     int
	toolCalls      int
	toolErrors     int
	repeatErrors   int
	userRejections int
	gaps           []float64
}

func sum(samples []Sample) totals {
	t := totals{sessions: len(samples)}
	for _, s := range samples {
		t.humanTurns += s.HumanTurns
		t.toolCalls += s.ToolCalls
		t.toolErrors += s.ToolErrors
		t.repeatErrors += s.RepeatErrors
		t.userRejections += s.UserRejections
		t.gaps = append(t.gaps, s.UninterruptedS...)
	}
	return t
}

// Compute produces the indicators derivable from session samples, comparing a
// current window against a baseline one.
func Compute(current, baseline []Sample) []Indicator {
	c, b := sum(current), sum(baseline)

	ratio := func(t totals, num, den float64) *float64 {
		if t.sessions == 0 || den == 0 {
			return nil
		}
		return f(num / den)
	}

	out := []Indicator{
		{
			Name:      "interrupts_per_session",
			Direction: "down",
			Proxy:     "human turns plus explicit tool rejections",
			Value:     ratio(c, float64(c.humanTurns+c.userRejections), float64(c.sessions)),
			Baseline:  ratio(b, float64(b.humanTurns+b.userRejections), float64(b.sessions)),
		},
		{
			Name:      "mean_uninterrupted_run",
			Unit:      "s",
			Direction: "up",
			Proxy: "wall clock between consecutive human turns; inflated by sessions " +
				"nobody came back to, so read the median",
			Value:    mean(c.gaps),
			Baseline: mean(b.gaps),
		},
		{
			Name:      "median_uninterrupted_run",
			Unit:      "s",
			Direction: "up",
			Proxy:     "as above, at the median",
			Value:     median(c.gaps),
			Baseline:  median(b.gaps),
		},
		{
			Name:      "tool_calls_per_human_turn",
			Direction: "up",
			Value:     ratio(c, float64(c.toolCalls), float64(c.humanTurns)),
			Baseline:  ratio(b, float64(b.toolCalls), float64(b.humanTurns)),
		},
		{
			Name:      "recurrence_rate",
			Direction: "down",
			Proxy: "share of failures whose normalized fingerprint already occurred " +
				"earlier in the same session",
			Value:    ratio(c, float64(c.repeatErrors), float64(c.toolErrors)),
			Baseline: ratio(b, float64(b.repeatErrors), float64(b.toolErrors)),
		},
	}
	for i := range out {
		if out[i].Value == nil && out[i].Unavailable == "" {
			out[i].Unavailable = "no sessions in this window yet"
		}
	}
	return out
}

// Pending are the indicators this project has defined and cannot yet measure.
//
// They are listed rather than omitted. An indicator that quietly disappears
// until the phase that fills it is an indicator nobody notices is missing, and
// the set of things being measured drifts to whatever happens to be easy.
func Pending() []Indicator {
	return []Indicator{
		{
			Name:        "false_block_rate",
			Direction:   "down",
			Unavailable: "nothing here refuses anything yet; there is no decision to be wrong",
		},
		{
			Name:        "context_tax",
			Direction:   "down",
			Unavailable: "nothing is injected into the agent's context yet",
		},
		{
			Name:        "resignation_rate",
			Direction:   "down",
			Unavailable: "needs the verification contract to tell giving up from finishing",
		},
		{
			Name:        "strategy_revision_rate",
			Direction:   "up",
			Unavailable: "needs the meta-state layer, whose effect it measures",
		},
		{
			Name:        "rediscovery_cost",
			Direction:   "down",
			Unavailable: "needs observed topology to know what had already been discovered",
		},
	}
}

func mean(xs []float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	var total float64
	for _, x := range xs {
		total += x
	}
	return f(total / float64(len(xs)))
}

func median(xs []float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return f(s[len(s)/2])
}

// Percentile returns the value at p (0..1) of a sorted copy of xs.
func Percentile(xs []float64, p float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	i := int(p * float64(len(s)-1))
	return f(s[i])
}
