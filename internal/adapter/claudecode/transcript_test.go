package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

// The figure this file guards is how much a person had to type. Three ways it
// could be wrong without anything failing to compile, and each is asserted
// below: counting bytes instead of characters, counting the runtime's own words
// as a person's, and losing a turn because its content arrived in the other of
// the two shapes the runtime uses.

func write(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	var body string
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHumanRunesCountsCharactersNotBytes(t *testing.T) {
	// Ten Japanese characters are thirty bytes in UTF-8. Reporting thirty would
	// say this session cost three times what the same sentence costs in English,
	// which is a statement about the encoding and not about the person.
	path := write(t,
		`{"type":"user","sessionId":"s","timestamp":"2026-08-30T00:00:00.000Z",`+
			`"message":{"content":"あいうえおかきくけこ"}}`)

	s, ok := readTranscript(path)
	if !ok {
		t.Fatal("transcript was not read")
	}
	if s.HumanRunes != 10 {
		t.Fatalf("HumanRunes = %d, want 10 (bytes would be 30)", s.HumanRunes)
	}
}

func TestHumanRunesReadsBothContentShapes(t *testing.T) {
	// The runtime writes a plain string for some turns and a block array for
	// others. A reader that handles one and silently scores the other as zero
	// would under-report exactly in proportion to how the person typed.
	path := write(t,
		`{"type":"user","sessionId":"s","timestamp":"2026-08-30T00:00:00.000Z",`+
			`"message":{"content":"abcde"}}`,
		`{"type":"user","sessionId":"s","timestamp":"2026-08-30T00:01:00.000Z",`+
			`"message":{"content":[{"type":"text","text":"fghij"}]}}`)

	s, ok := readTranscript(path)
	if !ok {
		t.Fatal("transcript was not read")
	}
	if s.HumanTurns != 2 {
		t.Fatalf("HumanTurns = %d, want 2", s.HumanTurns)
	}
	if s.HumanRunes != 10 {
		t.Fatalf("HumanRunes = %d, want 10 (5 from each shape)", s.HumanRunes)
	}
}

func TestHumanRunesExcludesWhatThePersonDidNotType(t *testing.T) {
	// Tool results are fed back in as user records, and sidechains are the
	// runtime talking to itself. Both would inflate the figure with volume no
	// person produced -- and tool results are the largest text in a transcript,
	// so the error would not be small.
	path := write(t,
		`{"type":"user","sessionId":"s","timestamp":"2026-08-30T00:00:00.000Z",`+
			`"message":{"content":"typed"}}`,
		`{"type":"user","sessionId":"s","timestamp":"2026-08-30T00:01:00.000Z",`+
			`"message":{"content":[{"type":"tool_result","content":"a very long tool result"}]}}`,
		`{"type":"user","isSidechain":true,"sessionId":"s","timestamp":"2026-08-30T00:02:00.000Z",`+
			`"message":{"content":"subagent chatter"}}`)

	s, ok := readTranscript(path)
	if !ok {
		t.Fatal("transcript was not read")
	}
	if s.HumanTurns != 1 {
		t.Fatalf("HumanTurns = %d, want 1", s.HumanTurns)
	}
	if s.HumanRunes != 5 {
		t.Fatalf("HumanRunes = %d, want 5 (only the typed turn)", s.HumanRunes)
	}
}
