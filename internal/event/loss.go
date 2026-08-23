package event

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Sh1n1230/prohairesis/internal/home"
)

// LossSchema is the contract for a record that an observation was lost.
const LossSchema = "harness.loss.v1"

// LossFile is where those records go, relative to the state directory.
const LossFile = "telemetry-loss.jsonl"

// Loss is one failure to record. Schema: harness.loss.v1.
//
// It carries no part of the event it stands for. That is not an oversight: a
// loss record holding the event would be a second event log, holding exactly
// what the first one refuses to hold, reached by a path with weaker guarantees.
type Loss struct {
	Schema    string    `json:"schema"`
	TS        time.Time `json:"ts"`
	SessionID string    `json:"session_id,omitempty"`
	Reason    string    `json:"reason"`
	Stage     string    `json:"stage"`
	Count     int       `json:"count,omitempty"`
}

// RecordLoss notes that an observation could not be written.
//
// The file lives at the top of the state directory, not beside the event log it
// stands for. The likeliest reason an event log cannot be written is that its
// session directory is missing, unwritable, or owned by someone else, and a
// sibling file fails for the same reason at the same moment. A second path is
// only a second path if it can fail independently.
//
// When even that write fails, the record goes to stderr, which the agent runtime
// captures. That is the last rung: it is not durable, and doctor cannot count
// what reached it. Saying so is better than pretending there is another rung.
func RecordLoss(sessionID, stage string, cause error) {
	l := Loss{
		Schema:    LossSchema,
		TS:        time.Now().UTC().Truncate(time.Millisecond),
		SessionID: sessionID,
		Reason:    cause.Error(),
		Stage:     stage,
		Count:     1,
	}
	if err := appendLoss(l); err != nil {
		l.Stage = "loss"
		l.Reason = fmt.Sprintf("%s; and the loss file itself: %v", cause, err)
		if b, mErr := json.Marshal(l); mErr == nil {
			fmt.Fprintf(os.Stderr, "prohairesis: %s\n", b)
		}
	}
}

func appendLoss(l Loss) error {
	p, err := home.Sub(LossFile)
	if err != nil {
		return err
	}
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	b = append(b, '\n')

	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	_, err = f.Write(b)
	return err
}

// LossCount reports how many observations are known to have been lost, and when
// the most recent one happened. doctor shows both: a log with holes in it is a
// log whose silence no longer means "nothing happened".
func LossCount() (int, time.Time, error) {
	d, err := home.Dir()
	if err != nil {
		return 0, time.Time{}, err
	}
	b, err := os.ReadFile(filepath.Join(d, LossFile))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, err
	}
	n := 0
	var last time.Time
	for _, line := range splitLines(b) {
		var l Loss
		if json.Unmarshal(line, &l) != nil {
			continue
		}
		c := l.Count
		if c == 0 {
			c = 1
		}
		n += c
		if l.TS.After(last) {
			last = l.TS
		}
	}
	return n, last, nil
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
