// Package checkpoint provides the reversibility substrate: point-in-time captures
// of a working tree that can be restored exactly, without the user's repository
// observing that anything happened.
//
// See docs/adr/0001-checkpoint-store-location.md for why the store lives outside
// the repository rather than in a ref namespace inside it.
package checkpoint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sh1n1230/prohairesis/internal/gitx"
)

// Store is a bare object database, outside the user's repository, holding the
// checkpoints for one session.
type Store struct {
	// Path is the bare repository directory, e.g. ~/.prohairesis/sessions/<id>/store.git
	Path string
	// RepoRoot is the working tree these checkpoints describe.
	RepoRoot string
}

const refPrefix = "refs/checkpoints/"

// Open initialises the store if needed and borrows the repository's existing
// objects, so unchanged content is never copied.
func Open(path, repoRoot string) (*Store, error) {
	s := &Store{Path: path, RepoRoot: repoRoot}

	if _, err := os.Stat(filepath.Join(path, "HEAD")); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := (gitx.Cmd{}).Run("init", "-q", "--bare", path); err != nil {
			return nil, fmt.Errorf("create checkpoint store: %w", err)
		}
	}

	gitDir, err := gitx.GitDirOf(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("locate repository: %w", err)
	}
	alts := filepath.Join(path, "objects", "info", "alternates")
	want := filepath.Join(gitDir, "objects")
	cur, _ := os.ReadFile(alts)
	if strings.TrimSpace(string(cur)) != want {
		if err := os.MkdirAll(filepath.Dir(alts), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(alts, []byte(want+"\n"), 0o644); err != nil {
			return nil, fmt.Errorf("borrow repository objects: %w", err)
		}
	}
	return s, nil
}

func (s *Store) cmd() gitx.Cmd { return gitx.Cmd{GitDir: s.Path} }

// writeTree captures the current working tree into a git tree object.
//
// It uses a throwaway index so the user's index is neither read nor written, and
// it honours .gitignore, so ignored paths are deliberately absent. Ignored files
// are the snapshot substrate's responsibility, not this layer's.
func (s *Store) writeTree(tag string) (string, error) {
	idx := filepath.Join(s.Path, "prohairesis-index."+tag)
	defer os.Remove(idx)
	if err := os.Remove(idx); err != nil && !os.IsNotExist(err) {
		return "", err
	}

	c := gitx.Cmd{GitDir: s.Path, WorkTree: s.RepoRoot, IndexFile: idx, Dir: s.RepoRoot}
	if err := c.Run("add", "-A"); err != nil {
		return "", fmt.Errorf("stage working tree: %w", err)
	}
	tree, err := c.Output("write-tree")
	if err != nil {
		return "", fmt.Errorf("write tree: %w", err)
	}
	return tree, nil
}

// Checkpoint captures the working tree and records it under a sequence number.
func (s *Store) Checkpoint(seq int, label string) (Record, error) {
	tree, err := s.writeTree(strconv.Itoa(seq))
	if err != nil {
		return Record{}, err
	}

	msg := fmt.Sprintf("prohairesis checkpoint %d", seq)
	if label != "" {
		msg += ": " + label
	}
	args := []string{"commit-tree", tree, "-m", msg}
	// Chain checkpoints so `git log` inside the store reads as a session history.
	if prev, err := s.head(seq - 1); err == nil && prev != "" {
		args = append(args, "-p", prev)
	}
	commit, err := s.cmd().Output(args...)
	if err != nil {
		return Record{}, fmt.Errorf("record checkpoint: %w", err)
	}
	if err := s.cmd().Run("update-ref", refPrefix+strconv.Itoa(seq), commit); err != nil {
		return Record{}, fmt.Errorf("publish checkpoint: %w", err)
	}
	return Record{Seq: seq, Commit: commit, Tree: tree, Label: label, At: time.Now().UTC()}, nil
}

func (s *Store) head(seq int) (string, error) {
	if seq < 1 {
		return "", nil
	}
	return s.cmd().Output("rev-parse", "-q", "--verify", refPrefix+strconv.Itoa(seq))
}

// Record is one checkpoint.
type Record struct {
	Seq    int       `json:"seq"`
	Commit string    `json:"commit"`
	Tree   string    `json:"tree,omitempty"`
	Label  string    `json:"label,omitempty"`
	At     time.Time `json:"at"`
}

// List returns every checkpoint in the store, oldest first.
func (s *Store) List() ([]Record, error) {
	lines, err := s.cmd().Lines("for-each-ref", "--format=%(refname) %(objectname) %(creatordate:unix)", refPrefix)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, ln := range lines {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		seq, err := strconv.Atoi(strings.TrimPrefix(f[0], refPrefix))
		if err != nil {
			continue
		}
		r := Record{Seq: seq, Commit: f[1]}
		if len(f) > 2 {
			if ts, err := strconv.ParseInt(f[2], 10, 64); err == nil {
				r.At = time.Unix(ts, 0).UTC()
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// Resolve turns a sequence number into a commit.
func (s *Store) Resolve(seq int) (string, error) {
	c, err := s.head(seq)
	if err != nil || c == "" {
		return "", fmt.Errorf("no checkpoint %d in %s", seq, s.Path)
	}
	return c, nil
}

// Verify reports objects the checkpoint needs that are no longer reachable.
//
// This matters because the store borrows the repository's object database. A
// history rewrite followed by garbage collection in the user's repository can
// remove a borrowed object. Restoring a partial tree silently would be worse
// than refusing, so Restore calls this first.
func (s *Store) Verify(commit string) ([]string, error) {
	objs, err := s.cmd().Output("rev-list", "--objects", commit)
	if err != nil {
		// When enough of the borrowed database is gone, rev-list cannot complete
		// the walk and fails outright instead of reporting per-object results.
		// That is still exactly the condition this function exists to detect, so
		// report it as missing objects rather than surfacing an opaque git error.
		if names := missingFromError(err); len(names) > 0 {
			return names, nil
		}
		return nil, err
	}
	if objs == "" {
		return nil, nil
	}
	var names []string
	for _, ln := range strings.Split(objs, "\n") {
		if f := strings.Fields(ln); len(f) > 0 {
			names = append(names, f[0])
		}
	}
	c := s.cmd()
	c.Stdin = []byte(strings.Join(names, "\n") + "\n")
	out, err := c.Output("cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "missing") {
			missing = append(missing, strings.Fields(ln)[0])
		}
	}
	return missing, nil
}

// objectNameRe matches the object names git quotes when it reports a missing one.
var objectNameRe = regexp.MustCompile(`'([0-9a-f]{40})'`)

// missingFromError extracts object names from a git failure that indicates the
// object database is incomplete. It returns nil for any other failure, so an
// unrelated error is never mistaken for missing data.
func missingFromError(err error) []string {
	var ge *gitx.Error
	if !errors.As(err, &ge) {
		return nil
	}
	s := ge.Stderr
	if !strings.Contains(s, "missing") && !strings.Contains(s, "does not exist") {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range objectNameRe.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		// git said the database is broken but named no object; report the
		// condition rather than silently treating the checkpoint as intact.
		out = append(out, "(object database incomplete)")
	}
	return out
}

// Change is one path difference between a checkpoint and the working tree.
type Change struct {
	Status string `json:"status"` // A added since checkpoint, D deleted, M modified
	Path   string `json:"path"`
}

// Diff compares a checkpoint against the current working tree.
//
// It works by capturing the current tree into the store and diffing two tree
// objects, which keeps the user's index entirely out of the operation.
func (s *Store) Diff(commit string) ([]Change, error) {
	cur, err := s.writeTree("diff")
	if err != nil {
		return nil, err
	}
	lines, err := s.cmd().Lines("diff-tree", "-r", "--name-status", commit, cur)
	if err != nil {
		return nil, err
	}
	var out []Change
	for _, ln := range lines {
		f := strings.SplitN(ln, "\t", 2)
		if len(f) != 2 {
			continue
		}
		out = append(out, Change{Status: f[0], Path: f[1]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Restore puts the working tree back to a checkpoint.
//
// Files the checkpoint does not contain are removed, but only if git considers
// them non-ignored: an ignored path such as a virtualenv or a data directory is
// never touched by this layer.
func (s *Store) Restore(commit string) error {
	missing, err := s.Verify(commit)
	if err != nil {
		return fmt.Errorf("verify checkpoint: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"refusing to restore: %d object(s) borrowed from the repository are gone "+
				"(history rewrite plus gc will do this). First missing: %s",
			len(missing), missing[0])
	}

	want, err := s.cmd().Lines("ls-tree", "-r", "--name-only", commit)
	if err != nil {
		return err
	}
	inCheckpoint := make(map[string]bool, len(want))
	for _, p := range want {
		inCheckpoint[p] = true
	}

	have, err := gitx.Cmd{Dir: s.RepoRoot}.Lines(
		"ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	for _, p := range have {
		if p == "" || inCheckpoint[p] {
			continue
		}
		if err := os.Remove(filepath.Join(s.RepoRoot, p)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}

	idx := filepath.Join(s.Path, "prohairesis-index.restore")
	defer os.Remove(idx)
	c := gitx.Cmd{GitDir: s.Path, WorkTree: s.RepoRoot, IndexFile: idx, Dir: s.RepoRoot}
	if err := c.Run("read-tree", commit); err != nil {
		return fmt.Errorf("load checkpoint tree: %w", err)
	}
	if err := c.Run("checkout-index", "-a", "-f", "-u"); err != nil {
		return fmt.Errorf("write working tree: %w", err)
	}
	pruneEmptyDirs(s.RepoRoot)
	return nil
}

// pruneEmptyDirs removes directories left behind by restore, best effort. A
// failure here is cosmetic and must never fail a restore.
func pruneEmptyDirs(root string) {
	var dirs []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.IsDir() {
			return nil //nolint:nilerr // best effort by design
		}
		if p == root || strings.Contains(p, string(os.PathSeparator)+".git") {
			return nil
		}
		dirs = append(dirs, p)
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		if entries, err := os.ReadDir(dirs[i]); err == nil && len(entries) == 0 {
			_ = os.Remove(dirs[i])
		}
	}
}
