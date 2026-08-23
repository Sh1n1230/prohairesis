package session

import (
	"testing"
	"time"
)

// The ownership rule is one line -- close only what the hook opened, and only
// once nobody is still using it -- and both halves of it protect something
// specific. These tests fix the line, because every plausible simplification of
// it breaks one of the two.

func TestHookOpensASessionAndClosesItsOwn(t *testing.T) {
	dir := newRepo(t)

	s, created, err := AttachOrCreate(dir, "test", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("AttachOrCreate did not create a session where none was open")
	}
	if !s.OwnedByHook() || s.AttachedCount() != 1 {
		t.Fatalf("owned=%v attached=%d; want true, 1", s.OwnedByHook(), s.AttachedCount())
	}

	if _, _, err := Detach(s.ID, "agent-1"); err != nil {
		t.Fatal(err)
	}
	closed, err := EndIfOwned(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("the hook did not close a session it opened and nobody was using")
	}
}

// A session started by hand is a statement that this piece of work should stay
// recoverable. An agent restarting is not a retraction of it.
func TestAHandStartedSessionSurvivesTheAgentEnding(t *testing.T) {
	dir := newRepo(t)
	manual, err := Start(dir, "manual")
	if err != nil {
		t.Fatal(err)
	}

	s, created, err := AttachOrCreate(dir, "test", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("AttachOrCreate started a second session while one was already open")
	}
	if s.ID != manual.ID {
		t.Fatalf("attached to %s, want %s", s.ID, manual.ID)
	}

	if _, _, err := Detach(s.ID, "agent-1"); err != nil {
		t.Fatal(err)
	}
	closed, err := EndIfOwned(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed {
		t.Fatal("the hook closed a session a human started; the point at which " +
			"they most want to go back is exactly the point it would have vanished")
	}
	if again, err := Current(dir); err != nil || again.ID != manual.ID {
		t.Fatalf("the hand-started session is no longer current: %v", err)
	}
}

// The case the invariant exists for: one agent opens the session, a second
// attaches, and the first ends. Closing then would stop checkpointing work that
// is still going on.
func TestASessionStaysOpenWhileAnotherAgentIsStillAttached(t *testing.T) {
	dir := newRepo(t)
	s, _, err := AttachOrCreate(dir, "test", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := AttachOrCreate(dir, "test", "agent-2"); err != nil {
		t.Fatal(err)
	}

	_, closable, err := Detach(s.ID, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if closable {
		t.Fatal("reported closable while another agent was still attached")
	}
	if closed, err := EndIfOwned(s.ID); err != nil || closed {
		t.Fatalf("closed=%v err=%v; the second agent is still working", closed, err)
	}

	if _, closable, err = Detach(s.ID, "agent-2"); err != nil {
		t.Fatal(err)
	}
	if !closable {
		t.Fatal("still not closable after the last agent detached")
	}
	if closed, err := EndIfOwned(s.ID); err != nil || !closed {
		t.Fatalf("closed=%v err=%v; want it closed now", closed, err)
	}
}

// The runtime may announce the same session more than once. A count cannot tell
// that from a second agent arriving; a set can.
func TestAttachingTwiceIsNotTwoAgents(t *testing.T) {
	dir := newRepo(t)
	s, _, err := AttachOrCreate(dir, "test", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := AttachOrCreate(dir, "test", "agent-1"); err != nil {
		t.Fatal(err)
	}
	s, err = Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.AttachedCount() != 1 {
		t.Fatalf("attached=%d after the same agent arrived twice; want 1", s.AttachedCount())
	}
}

func TestDebounceHoldsBackButNeverDefers(t *testing.T) {
	dir := newRepo(t)
	s, _, err := AttachOrCreate(dir, "test", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	if !s.DueForCheckpoint(true, now) {
		t.Fatal("a session with no checkpoint at all should take one immediately")
	}
	if _, err := CheckpointLocked(s.ID, "session start"); err != nil {
		t.Fatal(err)
	}
	s, err = Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}

	if s.DueForCheckpoint(true, now) {
		t.Fatal("checkpointed again immediately; diff becomes noise and the moment " +
			"worth returning to is buried among hundreds that are not")
	}
	if s.DueForCheckpoint(false, now.Add(2*DebounceInterval)) {
		t.Fatal("checkpointed after an action that could not have changed anything")
	}
	if !s.DueForCheckpoint(true, now.Add(2*DebounceInterval)) {
		t.Fatal("never checkpointed again; the interval spaces them out, it does not " +
			"stop them")
	}
}
