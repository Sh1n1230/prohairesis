// Package gitx is a thin, explicit wrapper around the git binary.
//
// Two rules govern everything here:
//
//   - We never mutate the user's repository state. No command in this package may
//     write to the user's index, HEAD, refs, reflog, stash or config. Operations that
//     need an index use a throwaway one via GIT_INDEX_FILE.
//   - Errors carry git's stderr. A silent git failure in a reversibility layer is
//     worse than no reversibility layer, because it produces false confidence.
package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Cmd describes one git invocation.
type Cmd struct {
	// Dir is the working directory for the process (-C equivalent).
	Dir string
	// GitDir, when set, becomes GIT_DIR: the object/ref database to operate on.
	GitDir string
	// WorkTree, when set, becomes GIT_WORK_TREE.
	WorkTree string
	// IndexFile, when set, becomes GIT_INDEX_FILE. Always point this at a
	// throwaway path when GitDir is a user repository.
	IndexFile string
	// Stdin, when non-nil, is piped to the process.
	Stdin []byte
}

// Error carries the command and git's own diagnostics.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	s := strings.TrimSpace(e.Stderr)
	if s == "" {
		return fmt.Sprintf("git %s: %v", strings.Join(e.Args, " "), e.Err)
	}
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.Args, " "), e.Err, s)
}

func (e *Error) Unwrap() error { return e.Err }

// Output runs git and returns trimmed stdout.
func (c Cmd) Output(args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = c.Dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if c.Stdin != nil {
		cmd.Stdin = bytes.NewReader(c.Stdin)
	}

	env := os.Environ()
	set := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	set("GIT_DIR", c.GitDir)
	set("GIT_WORK_TREE", c.WorkTree)
	set("GIT_INDEX_FILE", c.IndexFile)
	// Checkpoints are machine-local bookkeeping, not authored history. Pinning
	// identity and time keeps a checkpoint commit reproducible and keeps the
	// user's own identity out of artifacts they did not write.
	env = append(env,
		"GIT_AUTHOR_NAME=prohairesis", "GIT_AUTHOR_EMAIL=prohairesis@localhost",
		"GIT_COMMITTER_NAME=prohairesis", "GIT_COMMITTER_EMAIL=prohairesis@localhost",
		"GIT_TERMINAL_PROMPT=0",
	)
	cmd.Env = env

	if err := cmd.Run(); err != nil {
		return strings.TrimRight(stdout.String(), "\n"),
			&Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// Run is Output when the caller does not need stdout.
func (c Cmd) Run(args ...string) error {
	_, err := c.Output(args...)
	return err
}

// Lines runs git and splits stdout on newlines, dropping the trailing empty element.
func (c Cmd) Lines(args ...string) ([]string, error) {
	out, err := c.Output(args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// Available reports whether a usable git binary is on PATH.
func Available() (string, bool) {
	out, err := Cmd{}.Output("--version")
	if err != nil {
		return "", false
	}
	return strings.TrimPrefix(out, "git version "), true
}

// RepoRoot returns the working-tree root containing dir, or ok=false when dir is
// not inside a git working tree.
func RepoRoot(dir string) (string, bool) {
	out, err := Cmd{Dir: dir}.Output("rev-parse", "--show-toplevel")
	if err != nil || out == "" {
		return "", false
	}
	return out, true
}

// GitDirOf returns the absolute .git directory for the repository at root.
func GitDirOf(root string) (string, error) {
	return Cmd{Dir: root}.Output("rev-parse", "--absolute-git-dir")
}

// HeadOf returns the current HEAD commit, or "" for a repository with no commits.
func HeadOf(root string) string {
	out, err := Cmd{Dir: root}.Output("rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// RootCommit returns the earliest commit reachable from HEAD. It is used as a
// stable identity for a repository, so that moving or renaming a directory does
// not orphan its checkpoints. Returns "" when the repository has no commits.
func RootCommit(root string) string {
	out, err := Cmd{Dir: root}.Output("rev-list", "--max-parents=0", "HEAD")
	if err != nil || out == "" {
		return ""
	}
	// A repository may have several root commits; the last listed is the oldest.
	lines := strings.Split(out, "\n")
	return lines[len(lines)-1]
}
