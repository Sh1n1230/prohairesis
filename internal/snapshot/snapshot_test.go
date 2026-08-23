package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigAddRemove(t *testing.T) {
	c := &Config{}

	if !c.Add("data/interim") {
		t.Fatal("first Add reported no change")
	}
	if c.Add("data/interim") {
		t.Fatal("duplicate Add reported a change")
	}
	if c.Add("data/./interim") {
		t.Fatal("Add did not normalise an equivalent path")
	}
	if len(c.Paths) != 1 {
		t.Fatalf("Paths = %v, want one entry", c.Paths)
	}

	if !c.Remove("data/interim") {
		t.Fatal("Remove reported no change for a declared path")
	}
	if c.Remove("data/interim") {
		t.Fatal("Remove reported a change for an undeclared path")
	}
	if len(c.Paths) != 0 {
		t.Fatalf("Paths = %v, want empty", c.Paths)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	t.Setenv("PROHAIRESIS_HOME", t.TempDir())

	c, err := LoadConfig("repo-identity")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Paths) != 0 {
		t.Fatalf("a fresh config already has paths: %v", c.Paths)
	}
	if c.MaxBytes != DefaultMaxBytes {
		t.Fatalf("MaxBytes = %d, want the default %d", c.MaxBytes, DefaultMaxBytes)
	}

	c.Add("data")
	c.Add("artifacts")
	if err := c.Save("repo-identity"); err != nil {
		t.Fatal(err)
	}

	back, err := LoadConfig("repo-identity")
	if err != nil {
		t.Fatal(err)
	}
	// Save sorts, so the round trip is deterministic.
	if strings.Join(back.Paths, ",") != "artifacts,data" {
		t.Fatalf("Paths = %v, want [artifacts data]", back.Paths)
	}

	// Config is keyed by repository identity: a different repository sees nothing.
	other, err := LoadConfig("another-repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Paths) != 0 {
		t.Fatalf("config leaked across repositories: %v", other.Paths)
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCaptureAndRestore(t *testing.T) {
	repo := t.TempDir()
	writeTree(t, repo, map[string]string{
		"data/interim.bin":   "expensive",
		"data/nested/x.json": "{}",
		"junk/build.log":     "disposable",
	})

	c := &Config{MaxBytes: DefaultMaxBytes}
	c.Add("data")

	dst := filepath.Join(t.TempDir(), "snap")
	res, err := Capture(repo, dst, c)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Paths) != 1 || res.Paths[0] != "data" {
		t.Fatalf("captured %v, want [data]", res.Paths)
	}
	if res.Bytes == 0 {
		t.Fatal("Capture reported zero bytes")
	}

	// Only declared paths are captured. Nothing is captured by heuristic.
	if _, err := os.Stat(filepath.Join(dst, "junk")); !os.IsNotExist(err) {
		t.Fatal("an undeclared path was captured")
	}

	// Destroy, then restore.
	if err := os.RemoveAll(filepath.Join(repo, "data")); err != nil {
		t.Fatal(err)
	}
	done, err := Restore(repo, dst, c)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(done) != 1 {
		t.Fatalf("restored %v, want one path", done)
	}
	for rel, want := range map[string]string{
		"data/interim.bin":   "expensive",
		"data/nested/x.json": "{}",
	} {
		got, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}
}

// Restoring must replace the declared path wholesale, not merge into it, or a
// file the agent created after the checkpoint would survive an undo.
func TestRestoreReplacesRatherThanMerges(t *testing.T) {
	repo := t.TempDir()
	writeTree(t, repo, map[string]string{"data/keep.txt": "original"})

	c := &Config{MaxBytes: DefaultMaxBytes}
	c.Add("data")

	dst := filepath.Join(t.TempDir(), "snap")
	if _, err := Capture(repo, dst, c); err != nil {
		t.Fatal(err)
	}

	writeTree(t, repo, map[string]string{
		"data/keep.txt":  "modified",
		"data/added.txt": "appeared after the checkpoint",
	})

	if _, err := Restore(repo, dst, c); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(repo, "data/keep.txt"))
	if err != nil || string(got) != "original" {
		t.Fatalf("keep.txt = %q (err %v), want %q", got, err, "original")
	}
	if _, err := os.Stat(filepath.Join(repo, "data/added.txt")); !os.IsNotExist(err) {
		t.Fatal("a file created after the checkpoint survived the restore")
	}
}

// The ceiling exists so that large data directories are protected by denying
// writes rather than by copying them on every checkpoint.
func TestCaptureRefusesAboveCeiling(t *testing.T) {
	repo := t.TempDir()
	writeTree(t, repo, map[string]string{"data/big.bin": strings.Repeat("x", 4096)})

	c := &Config{MaxBytes: 1024}
	c.Add("data")

	_, err := Capture(repo, filepath.Join(t.TempDir(), "snap"), c)
	if err == nil {
		t.Fatal("Capture accepted a set above the ceiling")
	}
	if !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("error does not explain the ceiling: %v", err)
	}
}

// An absent declared path is reported, not fatal: a checkpoint must still be
// taken when the agent has not created the directory yet.
func TestCaptureSkipsAbsentPaths(t *testing.T) {
	repo := t.TempDir()
	writeTree(t, repo, map[string]string{"data/present.txt": "here"})

	c := &Config{MaxBytes: DefaultMaxBytes}
	c.Add("data")
	c.Add("not-created-yet")

	res, err := Capture(repo, filepath.Join(t.TempDir(), "snap"), c)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(res.Paths) != 1 || res.Paths[0] != "data" {
		t.Fatalf("captured %v, want only [data]", res.Paths)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "not-created-yet") {
		t.Fatalf("Skipped = %v, want the absent path reported", res.Skipped)
	}
}

func TestHuman(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KiB"},
		{5 << 20, "5.0 MiB"},
		{3 << 30, "3.0 GiB"},
	} {
		if got := human(tc.in); got != tc.want {
			t.Errorf("human(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
