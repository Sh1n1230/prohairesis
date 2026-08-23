// Package claudecode is the adapter for one agent runtime.
//
// It is the only place in this repository that is allowed to know that runtime
// exists. Everything below it speaks the canonical action vocabulary and would
// be unchanged by a second adapter. A test fixes that seam by refusing any
// mention of the runtime outside this package -- an abstraction nobody can check
// is an abstraction that has already leaked.
package claudecode

import (
	"encoding/json"
	"io"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/Sh1n1230/prohairesis/internal/event"
	"github.com/Sh1n1230/prohairesis/internal/pathx"
)

// Name is how this adapter records itself.
const Name = "claude-code"

// Grades are the adapter's capability grades, recorded on each event so that a
// reader can tell how complete the record can possibly be. C1 is session
// lifecycle; C3 is post-execution observation, which sees what happened and
// cannot see what was prevented.
const (
	GradeLifecycle = "C1"
	GradePostExec  = "C3"
)

// Hook is one hook invocation as the runtime describes it.
//
// Unknown fields are ignored rather than rejected: a runtime that adds a field
// must not be able to turn this hook into a failure, and a hook that fails is a
// hook that has started to matter.
type Hook struct {
	HookEventName string          `json:"hook_event_name"`
	SessionID     string          `json:"session_id"`
	CWD           string          `json:"cwd"`
	Transcript    string          `json:"transcript_path"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	Reason        string          `json:"reason"`
	Source        string          `json:"source"`
}

// ParseHook reads a hook payload. An unreadable payload yields an empty Hook and
// no error: the caller's job is to record what it can and get out of the way.
func ParseHook(r io.Reader) Hook {
	var h Hook
	b, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil || len(b) == 0 {
		return h
	}
	_ = json.Unmarshal(b, &h)
	return h
}

// kinds maps the runtime's tools onto the canonical vocabulary.
//
// Anything absent from this table is opaque, and opaque is a real answer. The
// alternative -- folding an unmapped tool into exec because most tools do
// something -- would report complete coverage of a surface nobody has checked.
var kinds = map[string]event.Kind{
	"Read":         event.KindRead,
	"Glob":         event.KindRead,
	"Grep":         event.KindRead,
	"NotebookRead": event.KindRead,

	"Write":        event.KindWrite,
	"Edit":         event.KindWrite,
	"MultiEdit":    event.KindWrite,
	"NotebookEdit": event.KindWrite,

	"Bash":       event.KindExec,
	"BashOutput": event.KindExec,
	"KillShell":  event.KindExec,

	"WebFetch":  event.KindNetwork,
	"WebSearch": event.KindNetwork,
}

// KindOf classifies a tool by name only.
//
// By name only is the point. Reading the command line to decide what a shell
// invocation "really" does is the substring-matching game this project measured
// and rejected: it is defeated by variables, here-documents, make targets and
// base64, and it would put the reading of commands inside the layer whose one
// promise is that it does not interpret anything.
func KindOf(tool string) event.Kind {
	if k, ok := kinds[tool]; ok {
		return k
	}
	return event.KindOpaque
}

// mutatingKinds are the actions that could have changed the working tree.
// Opaque is among them: treating an unmapped action as harmless would be
// assuming the coverage this project does not claim to have.
func Mutating(k event.Kind) bool {
	switch k {
	case event.KindWrite, event.KindDelete, event.KindExec, event.KindOpaque:
		return true
	}
	return false
}

// toolInput is the subset of a tool's arguments this adapter will look at.
// Everything else is deliberately not read.
type toolInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
	Command      string `json:"command"`
	URL          string `json:"url"`
}

// Action builds the canonical record of one tool call.
//
// What it keeps is bounded on purpose: which tool, what class of action, which
// paths, which host, and -- for a shell command -- the first word plus a digest
// of the whole. The digest answers "is this the same command as last time",
// which is the only question any metric here asks. The command line itself is
// never stored, so this log cannot become the place where secrets accumulate.
func (h Hook) Action(repoRoot, home string) *event.Action {
	if h.ToolName == "" {
		return nil
	}
	a := &event.Action{Kind: KindOf(h.ToolName), Tool: h.ToolName}

	var in toolInput
	if len(h.ToolInput) > 0 {
		_ = json.Unmarshal(h.ToolInput, &in)
	}

	for _, p := range []string{in.FilePath, in.NotebookPath, in.Path} {
		if p == "" {
			continue
		}
		a.Paths = append(a.Paths, NormalizePath(p, repoRoot, home))
	}
	if c := strings.TrimSpace(in.Command); c != "" {
		a.Command = firstWord(c)
		a.ArgvSHA256 = event.DigestArgv(c)
	}
	if in.URL != "" {
		if u, err := url.Parse(in.URL); err == nil {
			a.Host = u.Host
		}
	}
	return a
}

// Outcome reports what the runtime said happened. Absent when it said nothing:
// unknown and succeeded are different facts.
func (h Hook) Outcome() *event.Outcome {
	if len(h.ToolResponse) == 0 {
		return nil
	}
	var resp struct {
		Interrupted bool  `json:"interrupted"`
		IsError     *bool `json:"is_error"`
		Success     *bool `json:"success"`
	}
	if err := json.Unmarshal(h.ToolResponse, &resp); err != nil {
		return nil
	}
	status := "ok"
	if resp.Interrupted || (resp.IsError != nil && *resp.IsError) ||
		(resp.Success != nil && !*resp.Success) {
		status = "error"
	}
	return &event.Outcome{Status: status}
}

// NormalizePath writes a path the way the log should hold it: relative to the
// repository when it is inside one, and otherwise absolute with the home
// directory written as ~.
//
// Both halves matter. Relative paths are what "did the agent go back over ground
// it already covered" is asked in, and they survive the repository being moved.
// A path outside the repository is the single most important thing an observer
// can be told, so it is kept in full rather than collapsed into a marker -- with
// the home directory abbreviated, so that a log stays something its owner can
// paste into an issue.
func NormalizePath(p, repoRoot, home string) string {
	abs := p
	if !filepath.IsAbs(abs) && repoRoot != "" {
		abs = filepath.Join(repoRoot, p)
	}
	abs = filepath.Clean(abs)

	// Both sides are resolved before comparing: the runtime and git can name the
	// same file through different symbolic links, and a raw comparison would
	// report a file inside the repository as being outside it.
	if repoRoot != "" {
		if rel, err := filepath.Rel(pathx.Resolve(repoRoot), pathx.Resolve(abs)); err == nil &&
			!strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if home != "" {
		if rel, err := filepath.Rel(pathx.Resolve(home), pathx.Resolve(abs)); err == nil &&
			!strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return abs
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\n"); i >= 0 {
		s = s[:i]
	}
	// Keep the program, not the path it was found at: identity, not location.
	return filepath.Base(s)
}
