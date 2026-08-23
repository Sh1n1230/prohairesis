package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo builds a throwaway repository with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	env := []string{
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x",
	}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	if err := (Cmd{Dir: dir}).Run("init", "-q"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Cmd{Dir: dir}).Run("add", "-A"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := (Cmd{Dir: dir}).Run("commit", "-qm", "init"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return dir
}

func TestAvailable(t *testing.T) {
	v, ok := Available()
	if !ok {
		t.Skip("git is not installed")
	}
	if v == "" {
		t.Fatal("reported available but returned an empty version")
	}
}

func TestRepoRoot(t *testing.T) {
	repo := newRepo(t)
	sub := filepath.Join(repo, "nested", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root, ok := RepoRoot(sub)
	if !ok {
		t.Fatal("expected a repository root from inside the tree")
	}
	// macOS reaches temp directories through a symlink, so compare resolved paths.
	want, _ := filepath.EvalSymlinks(repo)
	got, _ := filepath.EvalSymlinks(root)
	if want != got {
		t.Fatalf("root = %q, want %q", got, want)
	}

	outside := t.TempDir()
	if _, ok := RepoRoot(outside); ok {
		t.Fatal("reported a repository root outside any working tree")
	}
}

func TestHeadAndRootCommit(t *testing.T) {
	repo := newRepo(t)

	head := HeadOf(repo)
	if len(head) != 40 {
		t.Fatalf("HeadOf = %q, want a full object name", head)
	}
	// With a single commit, HEAD is also the root commit.
	if root := RootCommit(repo); root != head {
		t.Fatalf("RootCommit = %q, want %q", root, head)
	}

	// A second commit must not change the repository's identity.
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Cmd{Dir: repo}).Run("add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := (Cmd{Dir: repo}).Run("commit", "-qm", "second"); err != nil {
		t.Fatal(err)
	}
	if root := RootCommit(repo); root != head {
		t.Fatalf("RootCommit changed after a new commit: %q, want %q", root, head)
	}
}

func TestHeadOfEmptyRepository(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	if err := (Cmd{Dir: dir}).Run("init", "-q"); err != nil {
		t.Fatal(err)
	}
	if head := HeadOf(dir); head != "" {
		t.Fatalf("HeadOf on an empty repository = %q, want empty", head)
	}
	if root := RootCommit(dir); root != "" {
		t.Fatalf("RootCommit on an empty repository = %q, want empty", root)
	}
}

// A silent git failure in a reversibility layer produces false confidence, so
// errors must carry git's own diagnostics.
func TestErrorCarriesStderr(t *testing.T) {
	dir := t.TempDir()
	_, err := (Cmd{Dir: dir}).Output("rev-parse", "--show-toplevel")
	if err == nil {
		t.Fatal("expected an error outside a repository")
	}
	var ge *Error
	if !as(err, &ge) {
		t.Fatalf("error is %T, want *gitx.Error", err)
	}
	if ge.Stderr == "" {
		t.Fatal("error carried no stderr")
	}
	if !strings.Contains(ge.Error(), "rev-parse") {
		t.Fatalf("error message omits the command: %q", ge.Error())
	}
}

// as is errors.As without importing errors into the test's surface.
func as(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// The index is the user's. Every operation that needs one must use a throwaway.
func TestIndexFileIsolation(t *testing.T) {
	repo := newRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	before, err := (Cmd{Dir: repo}).Output("status", "--porcelain=v1")
	if err != nil {
		t.Fatal(err)
	}

	idx := filepath.Join(t.TempDir(), "throwaway-index")
	if err := (Cmd{Dir: repo, IndexFile: idx}).Run("add", "-A"); err != nil {
		t.Fatalf("add with a throwaway index: %v", err)
	}

	after, err := (Cmd{Dir: repo}).Output("status", "--porcelain=v1")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("staging through a throwaway index changed the user's status:\nbefore %q\nafter  %q", before, after)
	}
	if _, err := os.Stat(idx); err != nil {
		t.Fatalf("throwaway index was not written where asked: %v", err)
	}
}
