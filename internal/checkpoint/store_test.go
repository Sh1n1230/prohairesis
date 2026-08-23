package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sh1n1230/prohairesis/internal/gitx"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := (gitx.Cmd{Dir: dir}).Output(args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// newRepo builds a repository holding all three categories git treats
// differently: tracked, untracked, and ignored.
func newRepo(t *testing.T) string {
	t.Helper()
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_SYSTEM": "/dev/null",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@x",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@x",
	} {
		t.Setenv(k, v)
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, ".gitignore", "ignored/\n")
	write(t, dir, "src/a.txt", "tracked\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "init")

	write(t, dir, "src/a.txt", "tracked\ndirty\n")
	write(t, dir, "untracked.txt", "untracked\n")
	write(t, dir, "ignored/blob.bin", "ignored\n")
	return dir
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openStore(t *testing.T, repo string) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "store.git"), repo)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestOpenBorrowsRepositoryObjects(t *testing.T) {
	repo := newRepo(t)
	s := openStore(t, repo)

	alts, err := os.ReadFile(filepath.Join(s.Path, "objects", "info", "alternates"))
	if err != nil {
		t.Fatalf("alternates not written: %v", err)
	}
	gitDir, _ := gitx.GitDirOf(repo)
	if strings.TrimSpace(string(alts)) != filepath.Join(gitDir, "objects") {
		t.Fatalf("alternates = %q, want the repository object directory", alts)
	}

	// Opening twice must be idempotent, not append a second line.
	if _, err := Open(s.Path, repo); err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	again, _ := os.ReadFile(filepath.Join(s.Path, "objects", "info", "alternates"))
	if strings.Count(string(again), "\n") != 1 {
		t.Fatalf("alternates accumulated entries: %q", again)
	}
}

func TestCheckpointCapturesTheRightCategories(t *testing.T) {
	repo := newRepo(t)
	s := openStore(t, repo)

	rec, err := s.Checkpoint(1, "first")
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if len(rec.Commit) != 40 || rec.Seq != 1 {
		t.Fatalf("Record = %+v, want seq 1 and a full object name", rec)
	}

	show := func(path string) string {
		out, _ := (gitx.Cmd{GitDir: s.Path}).Output("show", rec.Commit+":"+path)
		return out
	}
	if got := show("src/a.txt"); got != "tracked\ndirty" {
		t.Fatalf("tracked file = %q, want the dirty content", got)
	}
	if got := show("untracked.txt"); got != "untracked" {
		t.Fatalf("untracked file = %q, want it captured", got)
	}
	// Ignored paths are deliberately absent: they belong to the snapshot layer.
	if got := show("ignored/blob.bin"); got != "" {
		t.Fatalf("ignored file = %q, want it excluded from the git layer", got)
	}
}

// The whole point of the design in ADR 0001: the user's repository must not be
// able to tell that any of this happened.
func TestCheckpointLeavesRepositoryUntouched(t *testing.T) {
	repo := newRepo(t)
	s := openStore(t, repo)

	surface := func() string {
		return strings.Join([]string{
			git(t, repo, "log", "--oneline", "--all"),
			git(t, repo, "status", "--porcelain=v1"),
			git(t, repo, "stash", "list"),
			git(t, repo, "for-each-ref", "--format=%(refname)"),
			git(t, repo, "rev-parse", "HEAD"),
		}, "\n--\n")
	}

	before := surface()
	for i := 1; i <= 3; i++ {
		if _, err := s.Checkpoint(i, ""); err != nil {
			t.Fatalf("Checkpoint %d: %v", i, err)
		}
	}
	if after := surface(); before != after {
		t.Fatalf("checkpointing changed the repository surface:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	entries, err := os.ReadDir(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "prohairesis") || strings.Contains(e.Name(), "harness") {
			t.Fatalf("wrote %q inside the user's .git", e.Name())
		}
	}
}

func TestListAndResolve(t *testing.T) {
	repo := newRepo(t)
	s := openStore(t, repo)

	for i := 1; i <= 3; i++ {
		if _, err := s.Checkpoint(i, ""); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("List returned %d checkpoints, want 3", len(list))
	}
	for i, r := range list {
		if r.Seq != i+1 {
			t.Fatalf("List is not in sequence order: %+v", list)
		}
	}
	if _, err := s.Resolve(2); err != nil {
		t.Fatalf("Resolve(2): %v", err)
	}
	if _, err := s.Resolve(99); err == nil {
		t.Fatal("Resolve accepted a checkpoint that does not exist")
	}
}

func TestDiff(t *testing.T) {
	repo := newRepo(t)
	s := openStore(t, repo)
	rec, err := s.Checkpoint(1, "")
	if err != nil {
		t.Fatal(err)
	}

	changes, err := s.Diff(rec.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("Diff against an untouched tree = %v, want empty", changes)
	}

	write(t, repo, "src/a.txt", "changed\n")
	write(t, repo, "new.txt", "added\n")
	if err := os.Remove(filepath.Join(repo, "untracked.txt")); err != nil {
		t.Fatal(err)
	}

	changes, err = s.Diff(rec.Commit)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range changes {
		got[c.Path] = c.Status
	}
	for path, want := range map[string]string{
		"src/a.txt":     "M",
		"new.txt":       "A",
		"untracked.txt": "D",
	} {
		if got[path] != want {
			t.Errorf("Diff status for %s = %q, want %q (all: %v)", path, got[path], want, got)
		}
	}
}

func TestRestore(t *testing.T) {
	repo := newRepo(t)
	s := openStore(t, repo)
	rec, err := s.Checkpoint(1, "")
	if err != nil {
		t.Fatal(err)
	}

	// Destroy the tree the way an agent plausibly would.
	if err := os.RemoveAll(filepath.Join(repo, "src")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "untracked.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "stray.txt", "created after the checkpoint\n")
	write(t, repo, ".gitignore", "clobbered\n")

	if err := s.Restore(rec.Commit); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	for rel, want := range map[string]string{
		"src/a.txt":     "tracked\ndirty\n",
		"untracked.txt": "untracked\n",
		".gitignore":    "ignored/\n",
	} {
		got, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatalf("%s not restored: %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "stray.txt")); !os.IsNotExist(err) {
		t.Fatal("a file created after the checkpoint survived the restore")
	}
}

// Ignored paths belong to the snapshot layer. Restore must not delete them, or
// an undo would wipe a virtualenv or a data directory.
func TestRestoreLeavesIgnoredPathsAlone(t *testing.T) {
	repo := newRepo(t)
	s := openStore(t, repo)
	rec, err := s.Checkpoint(1, "")
	if err != nil {
		t.Fatal(err)
	}

	write(t, repo, "ignored/blob.bin", "changed after the checkpoint\n")
	write(t, repo, "ignored/new.bin", "also new\n")

	if err := s.Restore(rec.Commit); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{
		"ignored/blob.bin": "changed after the checkpoint\n",
		"ignored/new.bin":  "also new\n",
	} {
		got, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatalf("restore deleted an ignored path %s: %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("restore modified an ignored path %s: %q", rel, got)
		}
	}
}

func TestVerifyReportsNothingMissingForAFreshCheckpoint(t *testing.T) {
	repo := newRepo(t)
	s := openStore(t, repo)
	rec, err := s.Checkpoint(1, "")
	if err != nil {
		t.Fatal(err)
	}
	missing, err := s.Verify(rec.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("Verify reported %v missing on a fresh checkpoint", missing)
	}
}

// Borrowed objects can disappear if the user rewrites history and collects
// garbage. Restoring a partial tree silently would be worse than refusing, so
// the failure must be loud and must leave the tree alone.
func TestRestoreRefusesWhenBorrowedObjectsAreGone(t *testing.T) {
	repo := newRepo(t)
	s := openStore(t, repo)
	rec, err := s.Checkpoint(1, "")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate the loss by cutting the alternates link to the repository.
	alts := filepath.Join(s.Path, "objects", "info", "alternates")
	if err := os.WriteFile(alts, []byte(filepath.Join(t.TempDir(), "gone")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	write(t, repo, "src/a.txt", "modified\n")
	err = s.Restore(rec.Commit)
	if err == nil {
		t.Fatal("Restore proceeded with missing objects")
	}
	if !strings.Contains(err.Error(), "refusing to restore") {
		t.Fatalf("error does not say it refused: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(repo, "src/a.txt"))
	if string(got) != "modified\n" {
		t.Fatalf("a refused restore still modified the tree: %q", got)
	}
}
