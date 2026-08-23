package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Sh1n1230/prohairesis/internal/adapter/claudecode"
	"github.com/Sh1n1230/prohairesis/internal/event"
	"github.com/Sh1n1230/prohairesis/internal/session"
)

// cmdHook is what an agent runtime calls on every tool call and at both ends of
// a session.
//
// It has one hard obligation, stronger than recording anything: it must never
// make the thing it observes fail. So it returns no error, exits 0 on every
// path, and recovers from any panic. The registration adds `|| true` on top of
// that -- the same promise made twice, once inside the process and once outside
// it, because the failure this guards against includes the binary being missing
// or from another version, which no code in this file can catch.
func cmdHook(args []string) int {
	started := time.Now()
	defer func() {
		if r := recover(); r != nil {
			event.RecordLoss("", "events", fmt.Errorf("hook panicked: %v", r))
		}
	}()

	if len(args) == 0 {
		return 0
	}
	h := claudecode.ParseHook(os.Stdin)
	dir := session.ResolveDir(h.CWD)

	switch args[0] {
	case "session-start":
		observe(dir, h, event.SessionStart, started)
	case "post-tool":
		observe(dir, h, event.Tool, started)
	case "session-end":
		endSession(dir, h, started)
	}
	return 0
}

// bind finds the session this observation belongs to, creating one if the
// repository has none open.
//
// Creating one here is what closes the gap that made checkpointing something a
// human had to remember: work done without a session was work that could not be
// undone. Attaching to an existing one is what keeps a session someone started
// deliberately from being replaced by one nobody asked for.
func bind(dir string, h claudecode.Hook) (*session.Session, error) {
	s, created, err := session.AttachOrCreate(dir, claudecode.Name, h.SessionID)
	if err != nil {
		return nil, err
	}
	if created {
		// A session with no checkpoint can restore nothing, so the first one is
		// taken now rather than at the first change, and synchronously: an
		// effect that lands before its checkpoint is an effect outside the only
		// guarantee this project makes.
		if _, err := session.CheckpointLocked(s.ID, "session start"); err != nil {
			return s, err
		}
		return session.Load(s.ID)
	}
	return s, nil
}

func observe(dir string, h claudecode.Hook, kind event.Type, started time.Time) {
	s, err := bind(dir, h)
	if err != nil {
		// Not being in a repository is not a loss; there was never anything to
		// record. Anything else is.
		if _, repoErr := session.DescribeRepo(dir); repoErr == nil {
			event.RecordLoss("", "events", err)
		}
		return
	}

	e := event.New(kind, s.ID, actor(h, kind))
	e.RepoKey = s.Repo.Key()

	mutating := false
	if kind == event.Tool {
		e.Action = h.Action(s.Repo.Root, homeDir())
		e.Outcome = h.Outcome()
		if e.Action != nil {
			mutating = claudecode.Mutating(e.Action.Kind)
		}
	}

	if s.DueForCheckpoint(mutating, time.Now()) {
		if c, err := session.CheckpointLocked(s.ID, "auto"); err == nil {
			e.CheckpointRef = &event.CheckpointRef{Seq: c.Seq, Commit: c.Commit, TakenHere: true}
		} else {
			event.RecordLoss(s.ID, "events", fmt.Errorf("automatic checkpoint: %w", err))
		}
	}
	if e.CheckpointRef == nil {
		if c, ok := s.LastCheckpoint(); ok {
			e.CheckpointRef = &event.CheckpointRef{Seq: c.Seq, Commit: c.Commit}
		}
	}

	e.HookMS = int(time.Since(started).Milliseconds())
	sink(s).Append(e)
}

// endSession records the end, captures the tree one last time, and closes the
// session only if it is this program's to close.
func endSession(dir string, h claudecode.Hook, started time.Time) {
	s, err := bind(dir, h)
	if err != nil {
		return
	}

	// Taken on every path, including the one that leaves the session open: the
	// moment an agent stops is exactly the moment worth being able to return to.
	e := event.New(event.SessionEnd, s.ID, actor(h, event.SessionEnd))
	e.RepoKey = s.Repo.Key()
	if c, err := session.CheckpointLocked(s.ID, "session end"); err == nil {
		e.CheckpointRef = &event.CheckpointRef{Seq: c.Seq, Commit: c.Commit, TakenHere: true}
	} else {
		event.RecordLoss(s.ID, "events", fmt.Errorf("final checkpoint: %w", err))
	}

	e.HookMS = int(time.Since(started).Milliseconds())
	sink(s).Append(e)

	if _, _, err := session.Detach(s.ID, h.SessionID); err != nil {
		event.RecordLoss(s.ID, "events", err)
		return
	}
	if _, err := session.EndIfOwned(s.ID); err != nil {
		event.RecordLoss(s.ID, "events", err)
	}
}

func actor(h claudecode.Hook, kind event.Type) event.Actor {
	grade := claudecode.GradeLifecycle
	if kind == event.Tool {
		grade = claudecode.GradePostExec
	}
	return event.Actor{Adapter: claudecode.Name, AgentSessionID: h.SessionID, Grade: grade}
}

func sink(s *session.Session) *event.Sink {
	dir, err := s.EventDir()
	if err != nil {
		// Sink.Append records its own failure; give it a path that will fail
		// rather than inventing one that might succeed somewhere unexpected.
		return event.Open("", s.ID)
	}
	return event.Open(dir, s.ID)
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}
