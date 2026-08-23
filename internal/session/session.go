// Package session models one agent working session and its persisted state.
//
// A session is the unit of reversibility and, later, of observability and
// meta-state. Nothing about a session is written inside the user's repository.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sh1n1230/prohairesis/internal/checkpoint"
	"github.com/Sh1n1230/prohairesis/internal/gitx"
	"github.com/Sh1n1230/prohairesis/internal/home"
	"github.com/Sh1n1230/prohairesis/internal/snapshot"
)

// EnvSession lets an adapter pin the session for a subprocess.
const EnvSession = "PROHAIRESIS_SESSION"

// Repo identifies the working tree a session operates on.
type Repo struct {
	Root string `json:"root"`
	// RootCommit is a stable identity that survives moving or renaming the
	// directory. Empty for a repository with no commits.
	RootCommit  string `json:"root_commit,omitempty"`
	HeadAtStart string `json:"head_at_start,omitempty"`
	Branch      string `json:"branch,omitempty"`
}

// Session is the persisted record. Schema: harness.session.v1.
type Session struct {
	Schema  string     `json:"schema"`
	ID      string     `json:"id"`
	Started time.Time  `json:"started"`
	Ended   *time.Time `json:"ended,omitempty"`
	Adapter string     `json:"adapter"`
	Repo    Repo       `json:"repo"`

	// Lineage lets a later session inherit meta-state from an earlier one.
	// Populated in a later phase; carried here so the schema does not churn.
	ParentSession string `json:"parent_session,omitempty"`
	TaskID        string `json:"task_id,omitempty"`

	Checkpoints []Checkpoint `json:"checkpoints"`
}

// Checkpoint pairs the git-layer capture with the snapshot of declared-precious
// paths that git deliberately ignores. Both halves are needed to restore a tree
// as the agent actually left it.
type Checkpoint struct {
	checkpoint.Record
	Snapshot *snapshot.Result `json:"snapshot,omitempty"`
}

const schemaName = "harness.session.v1"

func newID() string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}

// Dir is the session's state directory.
func Dir(id string) (string, error) { return home.SubDir("sessions", id) }

func metaPath(id string) (string, error) {
	d, err := Dir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "meta.json"), nil
}

// pointerPath is the per-repository "current session" marker. Keying by
// repository identity rather than by path means two checkouts of the same
// project do not collide, and a moved directory keeps its session.
func pointerPath(r Repo) (string, error) {
	key := r.RootCommit
	if key == "" {
		key = r.Root
	}
	sum := sha256.Sum256([]byte(key))
	return home.Sub("current", hex.EncodeToString(sum[:8])+".session")
}

// Key is a stable identity for the repository, preferred over its path so that
// moving or renaming a directory does not orphan its state.
func (r Repo) Key() string {
	if r.RootCommit != "" {
		return r.RootCommit
	}
	return r.Root
}

// DescribeRepo inspects a directory and returns its repository identity.
func DescribeRepo(dir string) (Repo, error) {
	root, ok := gitx.RepoRoot(dir)
	if !ok {
		return Repo{}, fmt.Errorf("%s is not inside a git working tree; "+
			"the reversibility layer needs one", dir)
	}
	r := Repo{Root: root, RootCommit: gitx.RootCommit(root), HeadAtStart: gitx.HeadOf(root)}
	if b, err := (gitx.Cmd{Dir: root}).Output("rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		r.Branch = b
	}
	return r, nil
}

// Start creates a session for dir and marks it current for that repository.
func Start(dir, adapter string) (*Session, error) {
	repo, err := DescribeRepo(dir)
	if err != nil {
		return nil, err
	}
	s := &Session{
		Schema:  schemaName,
		ID:      newID(),
		Started: time.Now().UTC(),
		Adapter: adapter,
		Repo:    repo,
	}
	if _, err := Dir(s.ID); err != nil {
		return nil, err
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	p, err := pointerPath(repo)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, []byte(s.ID+"\n"), 0o644); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) save() error {
	p, err := metaPath(s.ID)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Load reads a session by id.
func Load(id string) (*Session, error) {
	p, err := metaPath(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no session %s", id)
		}
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("session %s is corrupt: %w", id, err)
	}
	return &s, nil
}

// ErrNoSession means no session is active for the given directory.
var ErrNoSession = errors.New("no active prohairesis session for this repository")

// Current resolves the session for dir: an explicit environment pin wins,
// otherwise the repository's current-session pointer.
func Current(dir string) (*Session, error) {
	if id := strings.TrimSpace(os.Getenv(EnvSession)); id != "" {
		return Load(id)
	}
	repo, err := DescribeRepo(dir)
	if err != nil {
		return nil, err
	}
	p, err := pointerPath(repo)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoSession
		}
		return nil, err
	}
	s, err := Load(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, ErrNoSession
	}
	if s.Ended != nil {
		return nil, ErrNoSession
	}
	return s, nil
}

// Store opens this session's checkpoint object store.
func (s *Session) Store() (*checkpoint.Store, error) {
	d, err := Dir(s.ID)
	if err != nil {
		return nil, err
	}
	return checkpoint.Open(filepath.Join(d, "store.git"), s.Repo.Root)
}

// SnapshotDir is where the declared-precious paths for one checkpoint live.
func (s *Session) SnapshotDir(seq int) (string, error) {
	d, err := Dir(s.ID)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "snapshot", strconv.Itoa(seq)), nil
}

// Checkpoint captures the working tree -- both the git-visible part and the
// declared-precious ignored part -- and records it on the session.
func (s *Session) Checkpoint(label string) (Checkpoint, error) {
	st, err := s.Store()
	if err != nil {
		return Checkpoint{}, err
	}
	seq := 1
	if n := len(s.Checkpoints); n > 0 {
		seq = s.Checkpoints[n-1].Seq + 1
	}
	rec, err := st.Checkpoint(seq, label)
	if err != nil {
		return Checkpoint{}, err
	}
	out := Checkpoint{Record: rec}

	cfg, err := snapshot.LoadConfig(s.Repo.Key())
	if err != nil {
		return out, err
	}
	if len(cfg.Paths) > 0 {
		dst, err := s.SnapshotDir(seq)
		if err != nil {
			return out, err
		}
		res, err := snapshot.Capture(s.Repo.Root, dst, cfg)
		if err != nil {
			// The git half is already durable. Report the failure rather than
			// discarding a checkpoint that is still useful.
			s.Checkpoints = append(s.Checkpoints, out)
			_ = s.save()
			return out, fmt.Errorf(
				"checkpoint %d recorded, but the precious-path snapshot failed: %w", seq, err)
		}
		out.Snapshot = &res
	}

	s.Checkpoints = append(s.Checkpoints, out)
	return out, s.save()
}

// RestoreSnapshot puts the declared-precious paths back from one checkpoint.
func (s *Session) RestoreSnapshot(seq int) ([]string, error) {
	cfg, err := snapshot.LoadConfig(s.Repo.Key())
	if err != nil || len(cfg.Paths) == 0 {
		return nil, err
	}
	src, err := s.SnapshotDir(seq)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil, nil
	}
	return snapshot.Restore(s.Repo.Root, src, cfg)
}

// End closes the session and clears the repository pointer.
func (s *Session) End() error {
	now := time.Now().UTC()
	s.Ended = &now
	if err := s.save(); err != nil {
		return err
	}
	p, err := pointerPath(s.Repo)
	if err != nil {
		return err
	}
	if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) == s.ID {
		_ = os.Remove(p)
	}
	return nil
}

// List returns all known sessions, newest first.
func List() ([]*Session, error) {
	root, err := home.SubDir("sessions")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s, err := Load(e.Name()); err == nil {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}
