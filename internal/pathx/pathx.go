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

import "path/filepath"

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
