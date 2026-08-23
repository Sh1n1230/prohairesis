package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/Sh1n1230/prohairesis/internal/adapter/claudecode"
	"github.com/Sh1n1230/prohairesis/internal/event"
	"github.com/Sh1n1230/prohairesis/internal/metrics"
	"github.com/Sh1n1230/prohairesis/internal/session"
)

// cmdMetrics answers the question this project has to be willing to lose:
// is there less friction than before, or not?
//
// It compares a window before this project started watching against the window
// since, both computed by the same code over the same inputs. The boundary is
// the earliest event ever recorded -- derived, not stored, so that it cannot
// disagree with the log it describes.
func cmdMetrics(args []string) error {
	_ = args

	observed, err := observedSessions()
	if err != nil {
		return err
	}

	root, err := claudecode.TranscriptRoot()
	if err != nil {
		return err
	}
	samples, err := claudecode.ReadTranscripts(root)
	if err != nil {
		return err
	}
	before, after := metrics.Split(samples, observed.since)

	fmt.Println("prohairesis metrics")
	fmt.Println()
	fmt.Println("coverage")
	if observed.since.IsZero() {
		fmt.Println("  nothing has been observed yet; no hooks are recording")
		fmt.Println("  run: prohairesis hooks install")
	} else {
		fmt.Printf("  observing since   %s\n", age(observed.since))
		fmt.Printf("  sessions observed %d in %d repositor%s\n",
			observed.sessions, len(observed.repos), plural(len(observed.repos)))
		for _, r := range observed.repos {
			fmt.Printf("    %s\n", r)
		}
		fmt.Println("  Work in any repository not listed is invisible here. These")
		fmt.Println("  figures describe the listed ones and nothing else.")
	}
	fmt.Printf("  transcripts       %d before, %d since\n", len(before), len(after))

	fmt.Println()
	fmt.Println("friction")
	for _, ind := range metrics.Compute(after, before) {
		printIndicator(ind)
	}

	fmt.Println()
	fmt.Println("recovery")
	fmt.Printf("  %-26s %s\n", "checkpoints", fmt.Sprint(observed.checkpoints))
	fmt.Printf("  %-26s %s\n", "automatic checkpoints", fmt.Sprint(observed.autoCheckpoints))

	fmt.Println()
	fmt.Println("this record's own integrity")
	fmt.Printf("  %-26s %s\n", "events recorded", fmt.Sprint(observed.events))
	fmt.Printf("  %-26s %s\n", "events known lost", fmt.Sprint(observed.lost+observed.gaps))
	if observed.lost+observed.gaps > 0 {
		fmt.Println("    a log with holes is a log whose silence proves nothing")
	}
	fmt.Printf("  %-26s p50 %s  p99 %s\n", "hook time",
		ms(metrics.Percentile(observed.hookMS, 0.50)),
		ms(metrics.Percentile(observed.hookMS, 0.99)))
	fmt.Println("    Recorded, not budgeted. There is no measured basis for a")
	fmt.Println("    threshold yet, and inventing one would repeat the mistake this")
	fmt.Println("    number exists to correct. See docs/METRICS.md.")

	fmt.Println()
	fmt.Println("defined, not yet measurable")
	for _, ind := range metrics.Pending() {
		printIndicator(ind)
	}
	return nil
}

func printIndicator(ind metrics.Indicator) {
	value := "n/a"
	if ind.Value != nil {
		value = fmt.Sprintf("%.3g%s", *ind.Value, ind.Unit)
	}
	line := fmt.Sprintf("  %-26s %-10s", ind.Name, value)
	if ind.Baseline != nil {
		line += fmt.Sprintf(" baseline %.3g%s", *ind.Baseline, ind.Unit)
	}
	if ind.Direction != "" {
		line += "  (" + ind.Direction + " is better)"
	}
	fmt.Println(line)
	if ind.Value == nil && ind.Unavailable != "" {
		fmt.Printf("    %s\n", ind.Unavailable)
	}
	if ind.Proxy != "" {
		fmt.Printf("    counted as: %s\n", ind.Proxy)
	}
}

// observation is what the event logs themselves say about their own coverage.
type observation struct {
	since           time.Time
	sessions        int
	repos           []string
	events          int
	gaps            int
	lost            int
	checkpoints     int
	autoCheckpoints int
	hookMS          []float64
}

func observedSessions() (observation, error) {
	var o observation
	all, err := session.List()
	if err != nil {
		return o, err
	}
	repos := map[string]bool{}
	for _, s := range all {
		dir, err := s.EventDir()
		if err != nil {
			continue
		}
		log, err := event.Read(dir)
		if err != nil || len(log.Events) == 0 {
			continue
		}
		o.sessions++
		o.events += len(log.Events)
		o.gaps += log.Gaps
		repos[s.Repo.Root] = true
		o.checkpoints += len(s.Checkpoints)
		for _, c := range s.Checkpoints {
			if c.Label == "auto" {
				o.autoCheckpoints++
			}
		}
		for _, e := range log.Events {
			o.hookMS = append(o.hookMS, float64(e.HookMS))
			if o.since.IsZero() || e.TS.Before(o.since) {
				o.since = e.TS
			}
		}
	}
	for r := range repos {
		o.repos = append(o.repos, r)
	}
	sort.Strings(o.repos)

	lost, _, err := event.LossCount()
	if err == nil {
		o.lost = lost
	}
	return o, nil
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
