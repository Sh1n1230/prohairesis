package session

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Ownership is the rule that decides who may close a session.
//
// A session started by hand is a statement of intent: "keep me able to get back
// to here for as long as this piece of work lasts". An agent restarting -- a
// cleared context, a crash, a new session after compaction -- must not retract
// that statement. A session the hook created for its own convenience has no such
// standing behind it, and leaving it open forever would let one session swell
// until diff and undo span unrelated work.
//
// So:
//
//	close  <=>  CreatedByHook && len(Attached) == 0
//
// Attached holds agent session identifiers rather than a count, because the
// runtime can announce the same session twice and a count cannot tell a repeat
// from a second arrival.
type Ownership struct {
	CreatedByHook bool     `json:"created_by_hook,omitempty"`
	Attached      []string `json:"attached,omitempty"`
}

// Attached reports how many agent sessions currently hold this session open.
func (s *Session) AttachedCount() int { return len(s.Attached) }

// OwnedByHook reports whether the hook created this session, and so whether it
// may close it.
func (s *Session) OwnedByHook() bool { return s.CreatedByHook }

// mayClose is the invariant above, in one place so that it cannot drift between
// the code that enforces it and the test that fixes it.
func (s *Session) mayClose() bool { return s.CreatedByHook && len(s.Attached) == 0 }

// AttachOrCreate binds an agent session to this repository's session, creating
// one only if none is open.
//
// It reports whether it created the session, which is the caller's cue to take a
// first checkpoint: a session with no checkpoint cannot restore anything.
func AttachOrCreate(dir, adapter, agentID string) (s *Session, created bool, err error) {
	repo, err := DescribeRepo(dir)
	if err != nil {
		return nil, false, err
	}
	lock, err := repoLock(repo)
	if err != nil {
		return nil, false, err
	}
	err = withLock(lock, func() error {
		cur, cerr := CurrentFor(repo)
		if cerr == nil {
			s = cur
		} else if cerr == ErrNoSession {
			n, serr := StartFor(repo, adapter)
			if serr != nil {
				return serr
			}
			n.CreatedByHook = true
			s, created = n, true
		} else {
			return cerr
		}
		if agentID != "" && !contains(s.Attached, agentID) {
			s.Attached = append(s.Attached, agentID)
		}
		return s.save()
	})
	if err != nil {
		return nil, false, err
	}
	return s, created, nil
}

// Detach releases one agent session's hold and reports whether the session may
// now be closed.
func Detach(id, agentID string) (s *Session, closable bool, err error) {
	lock, err := sessionLock(id)
	if err != nil {
		return nil, false, err
	}
	err = withLock(lock, func() error {
		cur, lerr := Load(id)
		if lerr != nil {
			return lerr
		}
		s = cur
		if agentID != "" {
			s.Attached = remove(s.Attached, agentID)
		}
		if err := s.save(); err != nil {
			return err
		}
		closable = s.mayClose() && s.Ended == nil
		return nil
	})
	return s, closable, err
}

// EndIfOwned closes the session when the ownership rule allows it, and reports
// whether it did. A session held open by another agent, or started by hand, is
// left alone.
func EndIfOwned(id string) (bool, error) {
	lock, err := sessionLock(id)
	if err != nil {
		return false, err
	}
	closed := false
	err = withLock(lock, func() error {
		s, lerr := Load(id)
		if lerr != nil {
			return lerr
		}
		if s.Ended != nil || !s.mayClose() {
			return nil
		}
		if err := s.End(); err != nil {
			return err
		}
		closed = true
		return nil
	})
	return closed, err
}

// CheckpointLocked takes a checkpoint under the session lock, so that a hook
// racing another hook cannot lose one from the record.
func CheckpointLocked(id, label string) (c Checkpoint, err error) {
	lock, lerr := sessionLock(id)
	if lerr != nil {
		return Checkpoint{}, lerr
	}
	err = withLock(lock, func() error {
		s, e := Load(id)
		if e != nil {
			return e
		}
		c, e = s.Checkpoint(label)
		return e
	})
	return c, err
}

// DebounceInterval is how long a session goes between automatic checkpoints
// while it is working.
//
// It is not a performance concession -- checkpoints are taken synchronously
// precisely so that no window exists in which an effect has happened and no
// checkpoint covers it. It is a legibility one: a checkpoint after every single
// tool call turns diff into noise and buries the moment worth going back to
// among hundreds that are not.
const DebounceInterval = 60 * time.Second

// DueForCheckpoint reports whether enough has happened, and enough time passed,
// for an automatic checkpoint.
//
// mutating is the caller's reading of whether the action could have changed the
// tree. An action nobody could map is treated as mutating: assuming otherwise
// would be assuming coverage this project does not have.
func (s *Session) DueForCheckpoint(mutating bool, now time.Time) bool {
	if !mutating || s.Ended != nil {
		return false
	}
	n := len(s.Checkpoints)
	if n == 0 {
		return true
	}
	return now.Sub(s.Checkpoints[n-1].At) >= DebounceInterval
}

// LastCheckpoint returns the most recent checkpoint, if any. It is the answer to
// "where would undo start from", which every event records.
func (s *Session) LastCheckpoint() (Checkpoint, bool) {
	if n := len(s.Checkpoints); n > 0 {
		return s.Checkpoints[n-1], true
	}
	return Checkpoint{}, false
}

// EventDir is where this session's observations are written.
func (s *Session) EventDir() (string, error) { return Dir(s.ID) }

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func remove(xs []string, x string) []string {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

// ResolveDir maps a directory reported by an adapter to something usable,
// falling back to the process working directory when the adapter said nothing.
func ResolveDir(reported string) string {
	reported = strings.TrimSpace(reported)
	if reported != "" {
		if fi, err := os.Stat(reported); err == nil && fi.IsDir() {
			return reported
		}
	}
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

// ErrNotInRepo is returned when observation is asked for outside a working tree.
var ErrNotInRepo = fmt.Errorf("not inside a git working tree")
