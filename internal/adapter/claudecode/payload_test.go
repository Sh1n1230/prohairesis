package claudecode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sh1n1230/prohairesis/internal/event"
)

func TestUnmappedToolsAreOpaqueRatherThanGuessedAt(t *testing.T) {
	for tool, want := range map[string]event.Kind{
		"Read":               event.KindRead,
		"Edit":               event.KindWrite,
		"Bash":               event.KindExec,
		"WebFetch":           event.KindNetwork,
		"Task":               event.KindOpaque,
		"mcp__anything__run": event.KindOpaque,
		"SomethingNewIn2027": event.KindOpaque,
	} {
		if got := KindOf(tool); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", tool, got, want)
		}
	}
}

// Folding an unknown tool into exec would report coverage of a surface nobody
// has checked; treating it as harmless would assume the same thing in the other
// direction. It is unknown, and it might have changed the tree.
func TestAnUnknownActionIsTreatedAsAbleToChangeThings(t *testing.T) {
	if !Mutating(event.KindOpaque) {
		t.Fatal("an unmappable action is assumed harmless; that is the coverage " +
			"illusion this project exists to avoid")
	}
	if Mutating(event.KindRead) {
		t.Fatal("reading is treated as mutating, so checkpoints fire for nothing")
	}
}

// The single most important property of this log: it answers "was this the same
// command as before" and cannot answer "what was it".
func TestTheCommandLineNeverReachesTheEvent(t *testing.T) {
	secret := "export API_TOKEN=hunter2 && curl https://example.invalid"
	h := Hook{
		ToolName:  "Bash",
		ToolInput: json.RawMessage(`{"command":` + quote(secret) + `}`),
	}
	a := h.Action("/repo", "/home/someone")
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "API_TOKEN") {
		t.Fatalf("the command line survived into the event: %s", b)
	}
	if a.Command != "export" {
		t.Fatalf("Command = %q, want the program name only", a.Command)
	}
	if len(a.ArgvSHA256) != 64 {
		t.Fatalf("no digest recorded, so two identical commands cannot be told apart")
	}

	same := Hook{ToolName: "Bash", ToolInput: json.RawMessage(`{"command":` + quote(secret) + `}`)}
	if same.Action("/repo", "/home/someone").ArgvSHA256 != a.ArgvSHA256 {
		t.Fatal("the same command produced two digests, so recurrence cannot be seen")
	}
}

func TestPathsAreRelativeInsideTheRepositoryAndVisibleOutsideIt(t *testing.T) {
	const repo, home = "/home/someone/work/project", "/home/someone"
	for in, want := range map[string]string{
		"/home/someone/work/project/src/a.go": "src/a.go",
		"src/a.go":                            "src/a.go",
		"/home/someone/.zshrc":                "~/.zshrc",
		"/etc/hosts":                          "/etc/hosts",
	} {
		if got := NormalizePath(in, repo, home); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// Reaching outside the repository is the one thing an observer most needs told.
// Collapsing it into a marker would discard exactly the event worth keeping.
func TestAPathOutsideTheRepositoryIsNotCollapsed(t *testing.T) {
	got := NormalizePath("/home/someone/.ssh/config", "/home/someone/work/project", "/home/someone")
	if !strings.Contains(got, ".ssh") {
		t.Fatalf("path outside the repository was flattened to %q", got)
	}
}

func TestAnUnreadablePayloadIsNotAFailure(t *testing.T) {
	h := ParseHook(strings.NewReader("this is not JSON"))
	if h.ToolName != "" || h.SessionID != "" {
		t.Fatal("garbage was parsed into something")
	}
	if a := h.Action("/repo", "/home"); a != nil {
		t.Fatal("an empty hook produced an action")
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
