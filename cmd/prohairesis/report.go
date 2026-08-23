package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/Sh1n1230/prohairesis/internal/event"
	"github.com/Sh1n1230/prohairesis/internal/metrics"
	"github.com/Sh1n1230/prohairesis/internal/session"
)

// cmdReport answers "what happened in that session?" for one session.
//
// It is a separate command from metrics, and stays one. The two have different
// inputs and different readers: this reads one event log to explain an afternoon
// to the person who lived it, while metrics reads every session against a
// baseline to answer whether this project is worth keeping. Folding the second
// into the first would make the judgement look like a convenience feature of the
// debugging tool.
func cmdReport(args []string) error {
	s, err := resolveSession(args)
	if err != nil {
		return err
	}
	dir, err := s.EventDir()
	if err != nil {
		return err
	}
	log, err := event.Read(dir)
	if err != nil {
		return err
	}

	fmt.Printf("session   %s\n", s.ID)
	fmt.Printf("repo      %s (%s)\n", s.Repo.Root, s.Repo.Branch)
	fmt.Printf("started   %s\n", s.Started.Format("2006-01-02 15:04:05Z"))
	if s.Ended != nil {
		fmt.Printf("ended     %s\n", s.Ended.Format("2006-01-02 15:04:05Z"))
	} else {
		fmt.Printf("open      %d agent session(s) attached\n", s.AttachedCount())
	}
	owner := "started by hand"
	if s.OwnedByHook() {
		owner = "opened by the hook"
	}
	fmt.Printf("owner     %s\n", owner)
	fmt.Printf("ckpts     %d\n", len(s.Checkpoints))

	fmt.Println()
	if len(log.Events) == 0 {
		fmt.Println("no events recorded")
		if !hookInstalledSomewhere(s) {
			fmt.Println("  no hooks are registered for this repository;")
			fmt.Println("  run: prohairesis hooks install")
		}
		return nil
	}

	byType := map[event.Type]int{}
	byKind := map[event.Kind]int{}
	errors := 0
	var hookMS []float64
	for _, e := range log.Events {
		byType[e.Type]++
		if e.Action != nil {
			byKind[e.Action.Kind]++
		}
		if e.Outcome != nil && e.Outcome.Status == "error" {
			errors++
		}
		hookMS = append(hookMS, float64(e.HookMS))
	}

	fmt.Printf("events    %d  (%d tool, %d errors)\n",
		len(log.Events), byType[event.Tool], errors)
	fmt.Print("actions   ")
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	for i, k := range kinds {
		if i > 0 {
			fmt.Print("  ")
		}
		fmt.Printf("%s %d", k, byKind[event.Kind(k)])
	}
	fmt.Println()

	// Recorded, and deliberately not compared against a budget: there is no
	// measured basis for one yet. See docs/METRICS.md.
	fmt.Printf("hook      p50 %s  p99 %s  (recorded, not a budget)\n",
		ms(metrics.Percentile(hookMS, 0.50)), ms(metrics.Percentile(hookMS, 0.99)))

	if log.Gaps > 0 || log.Unreadable > 0 {
		fmt.Printf("missing   %d event(s) never written, %d line(s) unreadable\n",
			log.Gaps, log.Unreadable)
		fmt.Println("          this log has holes; its silence does not mean nothing happened")
	}

	fmt.Println()
	fmt.Println("timeline (most recent last)")
	from := 0
	if n := len(log.Events); n > 20 {
		from = n - 20
		fmt.Printf("  ... %d earlier\n", from)
	}
	for _, e := range log.Events[from:] {
		fmt.Printf("  %s  %s\n", e.TS.Format("15:04:05"), describe(e))
	}
	return nil
}

func describe(e event.Event) string {
	switch e.Type {
	case event.SessionStart:
		return "session start"
	case event.SessionEnd:
		return "session end"
	}
	if e.Action == nil {
		return "tool"
	}
	line := fmt.Sprintf("%-8s %s", e.Action.Kind, e.Action.Tool)
	switch {
	case e.Action.Command != "":
		line += " " + e.Action.Command
	case e.Action.Host != "":
		line += " " + e.Action.Host
	case len(e.Action.Paths) > 0:
		line += " " + e.Action.Paths[0]
	}
	if e.Outcome != nil && e.Outcome.Status == "error" {
		line += "  [error]"
	}
	if e.CheckpointRef != nil && e.CheckpointRef.TakenHere {
		line += fmt.Sprintf("  [checkpoint %d]", e.CheckpointRef.Seq)
	}
	if e.Truncated {
		line += "  [truncated]"
	}
	return line
}

func ms(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.0fms", *v)
}

func age(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format("2006-01-02 15:04:05Z")
}

func hookInstalledSomewhere(s *session.Session) bool {
	ok, err := installedFor(s.Repo.Root)
	return err == nil && ok
}
