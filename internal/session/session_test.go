package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sh1n1230/prohairesis/internal/gitx"
	"github.com/Sh1n1230/prohairesis/internal/snapshot"
)

func newRepo(t *testing.T) string {
	t.Helper()
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_SYSTEM": "/dev/null",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@x",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@x",
	} {
		t.Setenv(k, v)
	}
	t.Setenv("PROHAIRESIS_HOME", t.TempDir())
	t.Setenv(EnvSession, "")

	dir := t.TempDir()
	if err := (gitx.Cmd{Dir: dir}).Run("init", "-q"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".gitignore", "data/\n")
	write(t, dir, "src/a.txt", "one\n")
	if err := (gitx.Cmd{Dir: dir}).Run("add", "-A"); err != nil {
		t.Fatal(err)
	}
	if err := (gitx.Cmd{Dir: dir}).Run("commit", "-qm", "init"); err != nil {
		t.Fatal(err)
	}
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

func TestRepoKeyPrefersIdentityOverPath(t *testing.T) {
	r := Repo{Root: "/some/path", RootCommit: "abc123"}
	if r.Key() != "abc123" {
		t.Fatalf("Key = %q, want the root commit", r.Key())
	}
	// A repository with no commits has no stable identity yet, so it falls back.
	r = Repo{Root: "/some/path"}
	if r.Key() != "/some/path" {
		t.Fatalf("Key = %q, want the path fallback", r.Key())
	}
}

func TestDescribeRepoRejectsNonRepository(t *testing.T) {
	t.Setenv("PROHAIRESIS_HOME", t.TempDir())
	if _, err := DescribeRepo(t.TempDir()); err == nil {
		t.Fatal("DescribeRepo accepted a directory outside any working tree")
	}
}

func TestLifecycle(t *testing.T) {
	repo := newRepo(t)

	if _, err := Current(repo); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Current before start = %v, want ErrNoSession", err)
	}

	s, err := Start(repo, "test")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.Schema != "harness.session.v1" {
		t.Fatalf("Schema = %q, want the portable contract name", s.Schema)
	}
	if s.Repo.RootCommit == "" {
		t.Fatal("session did not record a repository identity")
	}

	got, err := Current(repo)
	if err != nil {
		t.Fatalf("Current after start: %v", err)
	}
	if got.ID != s.ID {
		t.Fatalf("Current returned %s, want %s", got.ID, s.ID)
	}

	if err := s.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
	if _, err := Current(repo); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Current after end = %v, want ErrNoSession", err)
	}

	// An ended session is still loadable by id: the record outlives the pointer.
	back, err := Load(s.ID)
	if err != nil {
		t.Fatalf("Load after end: %v", err)
	}
	if back.Ended == nil {
		t.Fatal("Load returned a session with no end time")
	}
}

func TestCheckpointSequence(t *testing.T) {
	repo := newRepo(t)
	s, err := Start(repo, "test")
	if err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 3; i++ {
		rec, err := s.Checkpoint("")
		if err != nil {
			t.Fatalf("Checkpoint %d: %v", i, err)
		}
		if rec.Seq != i {
			t.Fatalf("Seq = %d, want %d", rec.Seq, i)
		}
	}
	if len(s.Checkpoints) != 3 {
		t.Fatalf("session holds %d checkpoints, want 3", len(s.Checkpoints))
	}

	// The sequence must survive a reload: it is derived from persisted state.
	back, err := Load(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := back.Checkpoint("")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != 4 {
		t.Fatalf("Seq after reload = %d, want 4", rec.Seq)
	}
}

// Without a declared precious path, checkpoints carry no snapshot: nothing is
// captured by heuristic.
func TestCheckpointWithoutProtectedPathsTakesNoSnapshot(t *testing.T) {
	repo := newRepo(t)
	s, err := Start(repo, "test")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.Checkpoint("")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Snapshot != nil {
		t.Fatalf("Snapshot = %+v, want none", rec.Snapshot)
	}
}

func TestCheckpointAndRestoreProtectedPath(t *testing.T) {
	repo := newRepo(t)
	write(t, repo, "data/interim.bin", "expensive\n")

	s, err := Start(repo, "test")
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := snapshot.LoadConfig(s.Repo.Key())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Add("data")
	if err := cfg.Save(s.Repo.Key()); err != nil {
		t.Fatal(err)
	}

	rec, err := s.Checkpoint("with data")
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if rec.Snapshot == nil || len(rec.Snapshot.Paths) != 1 {
		t.Fatalf("Snapshot = %+v, want one captured path", rec.Snapshot)
	}

	// git must still refuse to capture it: the two layers do not overlap.
	st, err := s.Store()
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := (gitx.Cmd{GitDir: st.Path}).Output("show", rec.Commit+":data/interim.bin"); out != "" {
		t.Fatal("an ignored path leaked into the git layer")
	}

	if err := os.RemoveAll(filepath.Join(repo, "data")); err != nil {
		t.Fatal(err)
	}
	done, err := s.RestoreSnapshot(rec.Seq)
	if err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	if len(done) != 1 {
		t.Fatalf("restored %v, want one path", done)
	}
	got, err := os.ReadFile(filepath.Join(repo, "data/interim.bin"))
	if err != nil || string(got) != "expensive\n" {
		t.Fatalf("data/interim.bin = %q (err %v)", got, err)
	}
}

func TestEnvironmentPinOverridesPointer(t *testing.T) {
	repo := newRepo(t)
	first, err := Start(repo, "test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Start(repo, "test")
	if err != nil {
		t.Fatal(err)
	}
	// Start moved the pointer to the second session.
	if cur, _ := Current(repo); cur.ID != second.ID {
		t.Fatalf("pointer did not follow the newest session")
	}
	// An explicit pin wins, so an adapter can bind a subprocess to its session.
	t.Setenv(EnvSession, first.ID)
	cur, err := Current(repo)
	if err != nil {
		t.Fatal(err)
	}
	if cur.ID != first.ID {
		t.Fatalf("Current = %s, want the pinned %s", cur.ID, first.ID)
	}
}

func TestListIsNewestFirst(t *testing.T) {
	repo := newRepo(t)
	var ids []string
	for i := 0; i < 3; i++ {
		s, err := Start(repo, "test")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID)
	}
	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("List returned %d sessions, want 3", len(all))
	}
	if all[0].ID != ids[len(ids)-1] {
		t.Fatalf("List is not newest first: got %s", all[0].ID)
	}
}

func TestLoadRejectsCorruptRecord(t *testing.T) {
	repo := newRepo(t)
	s, err := Start(repo, "test")
	if err != nil {
		t.Fatal(err)
	}
	d, err := Dir(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "meta.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Load(s.ID)
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("Load on a corrupt record = %v, want a corruption error", err)
	}
}
