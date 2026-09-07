package verify

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Sh1n1230/prohairesis/internal/pathx"
)

// Bounds on what one category may contribute to a result.
//
// The findings cap is not about disk. It is about the document staying something
// an agent can read inside a context window: a linter with four hundred
// complaints has already said everything the score needs at four.
const (
	maxFindings   = 25
	maxMessage    = 500
	maxTailLines  = 3
	maxOutputKept = 512 << 10
)

// diagnostic matches the one output format nearly every compiler, linter and
// type checker agrees on: `path:line[:col]: message`. The rust arrow prefix is
// included because it is the same format wearing a hat.
//
// The path must contain a `/` or a `.`, which is what keeps a timestamp like
// `10:00:00` or a message like `error: 3: bad` from being read as a location.
var diagnostic = regexp.MustCompile(
	`^[ \t]*(?:-->[ \t]*)?([A-Za-z0-9_@~+.\-/\\]*[./][A-Za-z0-9_@~+.\-/\\]*):(\d+)(?::(\d+))?(?::[ \t]*|[ \t]+|$)(.*)$`)

// scrubbers erase the parts of a message that change between two runs of the
// same failure. Every one of them is a deliberate loss of information: what is
// removed is exactly what would make an identical failure look new.
//
// They are ordered, and the order matters -- temporary paths are erased whole
// before the hex rule can chew on the random component of their names.
var scrubbers = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`/(?:private/)?(?:var/folders|tmp)/\S*`), "<tmp>"},
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`), "<ts>"},
	{regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}(?:\.\d+)?\b`), "<ts>"},
	{regexp.MustCompile(`0x[0-9a-fA-F]+`), "<addr>"},
	{regexp.MustCompile(`(?i)\bpid[ =:]+\d+`), "<pid>"},
	{regexp.MustCompile(`\b\d+(?:\.\d+)?\s?(?:ms|µs|us|ns|s)\b`), "<dur>"},
	{regexp.MustCompile(`\b[0-9a-f]{8,}\b`), "<hex>"},
}

// normalizer turns one check's output into findings.
type normalizer struct {
	root string
	home string
}

// findings extracts what a failed check reported.
//
// Two passes, in order of how much they are trusted. Lines that carry a location
// are the reliable ones: a tool that names a file and a line has told us
// precisely what it objected to. Only when no line does that does this fall back
// to the tail of the output, which is where runners conventionally put their
// verdict -- and the fallback carries the exit status, because "exit 2" and
// "exit 1" from the same tool are usually different failures.
func (n normalizer) findings(c Check, out string, exit int) ([]Finding, bool) {
	var found []Finding
	seen := map[string]bool{}
	truncated := false

	add := func(f Finding) {
		key := f.Severity + "\x1f" + f.Message + "\x1f" + f.Location
		if seen[key] {
			return
		}
		seen[key] = true
		if len(found) >= maxFindings {
			truncated = true
			return
		}
		found = append(found, f)
	}

	for _, line := range strings.Split(out, "\n") {
		m := diagnostic.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		loc := pathx.Relative(m[1], n.root, n.home) + ":" + m[2]
		if m[3] != "" {
			loc += ":" + m[3]
		}
		add(Finding{
			Severity: SeverityHigh,
			Message:  n.scrub(m[4]),
			Location: loc,
		})
	}
	if len(found) > 0 {
		return found, truncated
	}

	add(Finding{
		Severity: SeverityHigh,
		Message:  fmt.Sprintf("exit %d: %s", exit, n.scrub(tail(out))),
		Location: c.Source,
	})
	return found, truncated
}

// scrub removes what varies between two runs of the same failure, and shortens
// what is left. Absolute paths go first, so that a message naming a file in the
// repository reads the same on two different machines.
func (n normalizer) scrub(s string) string {
	s = strings.TrimSpace(s)
	if n.root != "" {
		s = strings.ReplaceAll(s, pathx.Resolve(n.root)+"/", "")
		s = strings.ReplaceAll(s, n.root+"/", "")
	}
	if n.home != "" {
		s = strings.ReplaceAll(s, pathx.Resolve(n.home), "~")
		s = strings.ReplaceAll(s, n.home, "~")
	}
	for _, sc := range scrubbers {
		s = sc.re.ReplaceAllString(s, sc.with)
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxMessage {
		// Cut on a rune boundary: a message ending in half a character is a
		// message that fingerprints differently depending on the encoding of
		// whatever happened to sit at the ceiling.
		cut := maxMessage
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}

// tail returns the last few non-empty lines of output.
func tail(out string) string {
	var kept []string
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0 && len(kept) < maxTailLines; i-- {
		if ln := strings.TrimSpace(lines[i]); ln != "" {
			kept = append([]string{ln}, kept...)
		}
	}
	if len(kept) == 0 {
		return "no output"
	}
	return strings.Join(kept, " / ")
}

// bound shortens captured output, keeping both ends.
//
// Both ends, because the two conventions disagree: linters put their findings
// first and a summary last, test runners put failures last. Keeping only one end
// would silently lose every failure from one half of the tools this runs.
func bound(b []byte) []byte {
	if len(b) <= maxOutputKept {
		return b
	}
	half := maxOutputKept / 2
	out := make([]byte, 0, maxOutputKept+64)
	out = append(out, b[:half]...)
	out = append(out, []byte("\n... output shortened ...\n")...)
	return append(out, b[len(b)-half:]...)
}
