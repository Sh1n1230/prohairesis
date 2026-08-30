package claudecode

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sh1n1230/prohairesis/internal/metrics"
)

// This file reads the runtime's own session transcripts.
//
// It exists because two of the figures that matter most -- how often a human had
// to step in, and how long the agent ran between those moments -- are not
// visible from the hooks this project installs. Post-execution observation sees
// tool calls; it does not see the human. The transcripts do.
//
// The definitions here are the ones tools/baseline.sh used to capture the
// pre-install baseline, ported so that both sides of every comparison are
// produced by the same code. A baseline computed one way and a current figure
// computed another is not a comparison, and the difference between them would be
// indistinguishable from the effect being measured.

// rejectMark is what the runtime writes when a human declines a tool call. The
// apostrophe it actually uses is U+2019; matching the stable substring instead
// avoids a disagreement between shells, editors and locales.
const rejectMark = "want to proceed with this tool use"

// TranscriptRoot is where the runtime keeps its transcripts.
func TranscriptRoot() (string, error) {
	if d := strings.TrimSpace(os.Getenv(EnvConfigDir)); d != "" {
		return filepath.Join(d, "projects"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claude", "projects"), nil
}

// ReadTranscripts walks the transcript root and summarizes every session it can
// read. Unreadable files are skipped rather than fatal: this is a measurement of
// what happened, and one malformed file is not a reason to report nothing.
func ReadTranscripts(root string) ([]metrics.Sample, error) {
	var out []metrics.Sample
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		s, ok := readTranscript(path)
		if ok {
			out = append(out, s)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return out, err
	}
	return out, nil
}

type record struct {
	Type        string          `json:"type"`
	IsSidechain bool            `json:"isSidechain"`
	SessionID   string          `json:"sessionId"`
	CWD         string          `json:"cwd"`
	Timestamp   string          `json:"timestamp"`
	Message     json.RawMessage `json:"message"`
}

type block struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	IsError bool            `json:"is_error"`
	Content json.RawMessage `json:"content"`
}

func readTranscript(path string) (metrics.Sample, bool) {
	f, err := os.Open(path)
	if err != nil {
		return metrics.Sample{}, false
	}
	defer func() { _ = f.Close() }()

	s := metrics.Sample{Source: path}
	records := 0
	seen := map[string]bool{}
	var humanTimes []time.Time

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var r record
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Type == "" {
			continue
		}
		records++
		if s.SessionID == "" {
			s.SessionID = r.SessionID
		}
		if s.CWD == "" {
			s.CWD = r.CWD
		}
		ts, hasTS := parseTS(r.Timestamp)
		if hasTS && (s.First.IsZero() || ts.Before(s.First)) {
			s.First = ts
		}

		blocks, isString := contentBlocks(r.Message)

		// A human turn is a user record that is not a tool result being fed
		// back in, and not a sidechain: those are the runtime talking to itself.
		if r.Type == "user" && !r.IsSidechain && (isString || !hasBlock(blocks, "tool_result")) {
			s.HumanTurns++
			s.HumanRunes += utf8.RuneCountInString(humanText(r.Message))
			if hasTS {
				humanTimes = append(humanTimes, ts)
			}
		}
		if r.Type == "assistant" {
			for _, b := range blocks {
				if b.Type == "tool_use" {
					s.ToolCalls++
				}
			}
		}
		for _, b := range blocks {
			if b.Type != "tool_result" || !b.IsError {
				continue
			}
			s.ToolErrors++
			text := blockText(b.Content)
			if strings.Contains(text, rejectMark) {
				s.UserRejections++
			}
			if fp := fingerprint(text); fp != "" {
				if seen[fp] {
					s.RepeatErrors++
				}
				seen[fp] = true
			}
		}
	}
	if records == 0 {
		return s, false
	}

	for i := 1; i < len(humanTimes); i++ {
		s.UninterruptedS = append(s.UninterruptedS,
			humanTimes[i].Sub(humanTimes[i-1]).Seconds())
	}
	return s, true
}

// humanText returns what a person actually typed in one turn.
//
// The caller decides which records are human turns; this only extracts their
// text, and it counts every one of them, including the turn that stated the
// task. Excluding that turn would be a rule invented here rather than derived,
// and the figure it feeds is a comparison of two windows treated identically --
// which is what makes a consistent rule matter more than a clever one.
func humanText(msg json.RawMessage) string {
	if len(msg) == 0 {
		return ""
	}
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(msg, &m) != nil {
		return ""
	}
	return blockText(m.Content)
}

func contentBlocks(msg json.RawMessage) ([]block, bool) {
	if len(msg) == 0 {
		return nil, false
	}
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(msg, &m) != nil || len(m.Content) == 0 {
		return nil, false
	}
	var asString string
	if json.Unmarshal(m.Content, &asString) == nil {
		return nil, true
	}
	var blocks []block
	if json.Unmarshal(m.Content, &blocks) != nil {
		return nil, false
	}
	return blocks, false
}

func hasBlock(blocks []block, kind string) bool {
	for _, b := range blocks {
		if b.Type == kind {
			return true
		}
	}
	return false
}

func blockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

var (
	rePath  = regexp.MustCompile(`/[a-z0-9_./-]+`)
	reHex   = regexp.MustCompile(`[0-9a-f]{8,}`)
	reNum   = regexp.MustCompile(`[0-9]+`)
	reSpace = regexp.MustCompile(`\s+`)
)

// fingerprint reduces a failure to something comparable across attempts.
//
// It is mechanical on purpose -- lowercase, strip paths, hexadecimal blobs and
// numbers, collapse whitespace, truncate. No model, no interpretation, nothing
// that could quietly start deciding which failures are "really" the same.
func fingerprint(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	s = strings.ToLower(s)
	s = rePath.ReplaceAllString(s, "<path>")
	s = reHex.ReplaceAllString(s, "<hex>")
	s = reNum.ReplaceAllString(s, "0")
	s = reSpace.ReplaceAllString(s, " ")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

func parseTS(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}
