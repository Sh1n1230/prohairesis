package event

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// MaxBytes bounds one serialized event, newline included.
//
// Two reasons, neither of them storage. An event that always fits in one write
// keeps one observation on one line even if the process dies mid-hook, and it
// bounds the cost of reading the tail of the file to find the last sequence
// number. Events that would exceed it are shortened and marked, never dropped:
// a silently shortened record is a record that lies.
const MaxBytes = 4096

// Sink appends events for one session.
//
// Every method here is best-effort by construction: Append returns nothing,
// because there is no failure it could report that a caller should act on. A
// hook that failed to record must still exit 0. What a caller cannot do is lose
// the fact of the failure -- that goes to a durable path of its own, outside the
// session directory, in loss.go.
type Sink struct {
	Path      string // sessions/<id>/events.jsonl
	SessionID string
}

// Open returns a sink writing to dir/events.jsonl. It creates nothing yet.
func Open(dir, sessionID string) *Sink {
	return &Sink{Path: filepath.Join(dir, "events.jsonl"), SessionID: sessionID}
}

// Append records one event. It assigns Seq, and it never fails: a write that
// cannot happen is recorded as a loss instead.
func (s *Sink) Append(e Event) {
	if e.Schema == "" {
		e.Schema = Schema
	}
	if err := s.append(&e); err != nil {
		RecordLoss(s.SessionID, "events", err)
	}
}

func (s *Sink) append(e *Event) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	f, err := os.OpenFile(s.Path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(s.Path), err)
	}
	defer f.Close()

	// The lock serializes concurrent hooks -- the runtime may run tool calls in
	// parallel -- so that sequence numbers cannot collide and lines cannot
	// interleave. It is advisory, which is all that is needed: every writer of
	// this file is this program.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock %s: %w", filepath.Base(s.Path), err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	if e.Seq == 0 {
		last, err := lastSeq(f)
		if err != nil {
			return err
		}
		e.Seq = last + 1
	}

	line, err := marshalBounded(e)
	if err != nil {
		return err
	}
	n, err := f.Write(line)
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(s.Path), err)
	}
	if n != len(line) {
		return fmt.Errorf("short write to %s: %d of %d bytes", filepath.Base(s.Path), n, len(line))
	}
	return nil
}

// marshalBounded serializes e, shortening it until it fits in MaxBytes.
//
// It sheds the only field that can grow without bound -- the path list -- and
// says so in the record. Nothing else is negotiable: an event that cannot be
// written even with no paths at all is a bug, and is reported as one rather than
// quietly reshaped.
func marshalBounded(e *Event) ([]byte, error) {
	line, err := encode(e)
	if err != nil {
		return nil, err
	}
	if len(line) <= MaxBytes {
		return line, nil
	}
	if e.Action == nil || len(e.Action.Paths) == 0 {
		return nil, fmt.Errorf("event is %d bytes, over the %d-byte ceiling, with nothing droppable",
			len(line), MaxBytes)
	}

	e.Truncated = true
	for len(e.Action.Paths) > 0 {
		e.Action.Paths = e.Action.Paths[:len(e.Action.Paths)-1]
		line, err = encode(e)
		if err != nil {
			return nil, err
		}
		if len(line) <= MaxBytes {
			return line, nil
		}
	}
	return nil, fmt.Errorf("event is %d bytes with no paths left, over the %d-byte ceiling",
		len(line), MaxBytes)
}

func encode(e *Event) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return nil, fmt.Errorf("encode event: %w", err)
	}
	return buf.Bytes(), nil
}

// lastSeq reads the sequence number of the final complete line.
//
// Reading the tail rather than keeping a counter in a second file is deliberate:
// a counter is a second copy of a fact the log already holds, and the two drift
// exactly when something has gone wrong and the truth matters most.
func lastSeq(f *os.File) (int, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat event log: %w", err)
	}
	size := info.Size()
	if size == 0 {
		return 0, nil
	}
	// One event cannot exceed MaxBytes, so the last complete line always starts
	// within this window.
	window := int64(MaxBytes + 1)
	off := size - window
	if off < 0 {
		off = 0
		window = size
	}
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, off); err != nil {
		return 0, fmt.Errorf("read event log tail: %w", err)
	}

	buf = bytes.TrimRight(buf, "\n")
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		buf = buf[i+1:]
	} else if off > 0 {
		// The window holds no line boundary, so this fragment may be partial.
		return 0, fmt.Errorf("event log tail has no line boundary")
	}

	var tail struct {
		Seq int `json:"seq"`
	}
	if err := json.Unmarshal(buf, &tail); err != nil {
		return 0, fmt.Errorf("last line of the event log is not readable: %w", err)
	}
	return tail.Seq, nil
}
