package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Scope is where a hook registration lives.
//
// Project is the scope to try in: it changes one repository, it is visible in a
// place its owner already looks, and undoing it is deleting a file. User is
// where it belongs once it has earned staying -- installed once, in effect
// everywhere, which is what machine-level infrastructure means. The cost of the
// project scope is real and is not hidden: observation stops at the edge of that
// repository, and doctor says so.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
)

// EnvConfigDir is the runtime's own override for where user settings live. It is
// honoured so that this can be exercised against a throwaway configuration
// without touching the one in daily use.
const EnvConfigDir = "CLAUDE_CONFIG_DIR"

// marker identifies a hook entry as ours. Every command written here contains
// it, and nothing else is ever removed.
const marker = "prohairesis hook "

// hookEvents are the three points this project observes.
//
// Pre-execution is deliberately absent. The record of what was refused can be
// recovered from the runtime's own transcript, so wiring a hook there would buy
// convenience -- and it would put this program on the path of every tool call,
// one short step from deciding whether the call proceeds. That step is a later
// phase's, taken deliberately, not one to drift into for a metric already
// available elsewhere.
var hookEvents = []struct {
	Event   string
	Command string
	Matcher string
}{
	{Event: "SessionStart", Command: "session-start"},
	{Event: "SessionEnd", Command: "session-end"},
	{Event: "PostToolUse", Command: "post-tool", Matcher: "*"},
}

// SettingsPath resolves the settings file for a scope.
func SettingsPath(scope Scope, repoRoot string) (string, error) {
	switch scope {
	case ScopeProject:
		if repoRoot == "" {
			return "", fmt.Errorf("the project scope needs a repository")
		}
		return filepath.Join(repoRoot, ".claude", "settings.json"), nil
	case ScopeUser:
		if d := strings.TrimSpace(os.Getenv(EnvConfigDir)); d != "" {
			return filepath.Join(d, "settings.json"), nil
		}
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, ".claude", "settings.json"), nil
	}
	return "", fmt.Errorf("unknown scope %q", scope)
}

// command is the shell line registered for one hook.
//
// The `|| true` is not decoration. It is the outer half of a promise made twice:
// the process itself recovers from any panic and exits 0, and the shell discards
// the exit status even if the binary is missing, unreadable, or from a different
// version. A recording layer that can fail a tool call has stopped being a
// recording layer.
func command(bin, sub string) string {
	return fmt.Sprintf("%s hook %s || true", bin, sub)
}

// Plan is what an install or uninstall would write, computed before anything is
// touched so that --dry-run and the real thing cannot disagree.
type Plan struct {
	Path    string
	Before  []byte
	After   []byte
	Changed bool
}

// Install adds this project's hooks to the settings file for a scope.
func Install(scope Scope, repoRoot, bin string) (Plan, error) {
	return edit(scope, repoRoot, func(hooks *object, indent string) error {
		for _, h := range hookEvents {
			entry := map[string]any{
				"hooks": []map[string]string{{
					"type":    "command",
					"command": command(bin, h.Command),
				}},
			}
			if h.Matcher != "" {
				entry["matcher"] = h.Matcher
			}
			raw, err := json.MarshalIndent(entry, indent+indent+indent, indent)
			if err != nil {
				return err
			}
			if err := addEntry(hooks, h.Event, raw, indent); err != nil {
				return err
			}
		}
		return nil
	})
}

// Uninstall removes them, and only them.
func Uninstall(scope Scope, repoRoot string) (Plan, error) {
	return edit(scope, repoRoot, func(hooks *object, indent string) error {
		for _, k := range append([]string(nil), hooks.keys...) {
			raw, _ := hooks.get(k)
			kept, err := withoutOurs(raw)
			if err != nil {
				return err
			}
			if len(kept) == 0 {
				hooks.delete(k)
				continue
			}
			hooks.set(k, encodeArray(kept, indent))
		}
		return nil
	})
}

// Installed reports whether every hook this project registers is present in a
// scope. Partly installed counts as not installed: a half-wired set of hooks
// produces a log with holes that nothing marks as holes.
func Installed(scope Scope, repoRoot string) (bool, error) {
	path, err := SettingsPath(scope, repoRoot)
	if err != nil {
		return false, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	root, err := parseObject(b)
	if err != nil {
		return false, err
	}
	rawHooks, ok := root.get("hooks")
	if !ok {
		return false, nil
	}
	hooks, err := parseObject(rawHooks)
	if err != nil {
		return false, err
	}
	for _, h := range hookEvents {
		raw, ok := hooks.get(h.Event)
		if !ok || !bytes.Contains(raw, []byte(marker)) {
			return false, nil
		}
	}
	return true, nil
}

// edit applies a change to the hooks block of a settings file and returns what
// would be written, without writing it.
func edit(scope Scope, repoRoot string, apply func(hooks *object, indent string) error) (Plan, error) {
	path, err := SettingsPath(scope, repoRoot)
	if err != nil {
		return Plan{}, err
	}
	before, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return Plan{}, err
	}
	indent := detectIndent(before)

	root, err := parseObject(before)
	if err != nil {
		return Plan{}, fmt.Errorf("%s is not a JSON object this can edit safely: %w", path, err)
	}
	hooks := newObject()
	if raw, ok := root.get("hooks"); ok {
		if hooks, err = parseObject(raw); err != nil {
			return Plan{}, fmt.Errorf("the hooks block in %s is not an object: %w", path, err)
		}
	}
	if err := apply(hooks, indent); err != nil {
		return Plan{}, err
	}

	if hooks.empty() {
		root.delete("hooks")
	} else {
		raw, err := hooks.encode(indent + indent)
		if err != nil {
			return Plan{}, err
		}
		// The nested object is emitted at one level in; its closing brace has to
		// sit at the parent's indentation rather than at the start of the line.
		raw = bytes.TrimRight(raw, "\n")
		raw = bytes.Replace(raw, []byte("\n}"), []byte("\n"+indent+"}"), 1)
		root.set("hooks", raw)
	}

	after, err := root.encode(indent)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Path: path, Before: before, After: after,
		Changed: !bytes.Equal(before, after)}, nil
}

// addEntry appends one of our entries to a hook event, replacing any earlier one
// so that installing twice leaves the file exactly as installing once did.
func addEntry(hooks *object, event string, entry json.RawMessage, indent string) error {
	var kept []json.RawMessage
	if raw, ok := hooks.get(event); ok {
		var err error
		if kept, err = withoutOurs(raw); err != nil {
			return err
		}
	}
	kept = append(kept, entry)
	hooks.set(event, encodeArray(kept, indent))
	return nil
}

// withoutOurs returns the entries of a hook array that this project did not
// write, byte for byte as they were found.
func withoutOurs(raw json.RawMessage) ([]json.RawMessage, error) {
	var all []json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("a hook list is not an array: %w", err)
	}
	kept := make([]json.RawMessage, 0, len(all))
	for _, e := range all {
		if bytes.Contains(e, []byte(marker)) {
			continue
		}
		kept = append(kept, e)
	}
	return kept, nil
}

func encodeArray(items []json.RawMessage, indent string) json.RawMessage {
	if len(items) == 0 {
		return json.RawMessage("[]")
	}
	var b bytes.Buffer
	b.WriteString("[\n")
	for i, it := range items {
		b.WriteString(indent + indent + indent)
		b.Write(it)
		if i < len(items)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(indent + indent)
	b.WriteByte(']')
	return b.Bytes()
}

// Write commits a plan to disk, creating the directory if the file is new.
//
// A settings file left holding nothing but {} is removed rather than written: to
// the runtime the two are the same, and the empty file is a trace of having been
// here that uninstall has no reason to leave.
func (p Plan) Write() error {
	if !p.Changed {
		return nil
	}
	if string(bytes.TrimSpace(p.After)) == "{}" {
		if err := os.Remove(p.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
		return err
	}
	tmp := p.Path + ".prohairesis.tmp"
	if err := os.WriteFile(tmp, p.After, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.Path)
}
