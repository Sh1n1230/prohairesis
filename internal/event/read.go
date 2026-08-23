package event

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// Log is one session's events, plus what is known to be missing from them.
type Log struct {
	Events []Event
	// Gaps counts sequence numbers that no line accounts for. The log records
	// its own holes so that a reader is never left to mistake absence for
	// quiet: this layer's only promise is that what it did not see, it says it
	// did not see.
	Gaps int
	// Unreadable counts lines that could not be parsed -- a process killed
	// mid-write leaves at most one.
	Unreadable int
}

// Read loads the event log from a session directory. A missing file is an empty
// log, not an error: a session that never ran a tool never created one.
func Read(dir string) (Log, error) {
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return Log{}, nil
		}
		return Log{}, err
	}
	defer func() { _ = f.Close() }()

	var out Log
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, MaxBytes), MaxBytes*2)
	expect := 1
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			out.Unreadable++
			continue
		}
		if e.Seq > expect {
			out.Gaps += e.Seq - expect
		}
		if e.Seq >= expect {
			expect = e.Seq + 1
		}
		out.Events = append(out.Events, e)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
}
