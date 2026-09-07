// Package pathx holds the path handling that has to agree everywhere.
//
// It exists for one reason. On macOS the temporary and home directories are
// reached through symbolic links, so a path the agent reports and the path git
// reports for the same file are different strings. Compare them raw and a file
// inside the repository looks like a file outside it -- which turns a relative
// path into an absolute one in the log, and a repository-local write into
// something that reads like it escaped.
//
// The rule lives in one place so that the reversibility layer and the
// observability layer cannot disagree about where a file is.
package pathx

import (
	"path/filepath"
	"strings"
)

// Resolve follows symbolic links as far as the path exists, leaving the rest
// intact. A path that does not exist yet still resolves through the parents that
// do, which is what makes it usable on a file about to be created.
func Resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent, base := filepath.Split(p)
	if parent == "" || parent == p {
		return p
	}
	return filepath.Join(Resolve(filepath.Clean(parent)), base)
}

// Relative writes a path the way a record should hold it: relative to the
// repository when it is inside one, and otherwise absolute with the home
// directory written as ~.
//
// Both halves matter. Relative paths are what "did the agent go back over ground
// it already covered" is asked in, and they survive the repository being moved.
// A path outside the repository is the single most important thing an observer
// can be told, so it is kept in full rather than collapsed into a marker -- with
// the home directory abbreviated, so that a record stays something its owner can
// paste into an issue.
//
// It lives here rather than in the observability layer because the verification
// layer needs the same answer for the locations a tool reports, and two layers
// that disagree about where a file is would produce two records of one fact.
func Relative(p, repoRoot, home string) string {
	abs := p
	if !filepath.IsAbs(abs) && repoRoot != "" {
		abs = filepath.Join(repoRoot, p)
	}
	abs = filepath.Clean(abs)

	// Both sides are resolved before comparing: a tool and git can name the same
	// file through different symbolic links, and a raw comparison would report a
	// file inside the repository as being outside it.
	if repoRoot != "" {
		if rel, err := filepath.Rel(Resolve(repoRoot), Resolve(abs)); err == nil &&
			!strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if home != "" {
		if rel, err := filepath.Rel(Resolve(home), Resolve(abs)); err == nil &&
			!strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return abs
}
