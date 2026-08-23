package session

import (
	"os"
	"path/filepath"
	"syscall"
)

// withLock serializes read-modify-write on a piece of session state.
//
// The lock is held on a file of its own rather than on the state file, because
// saving replaces the state file by rename: a lock taken on the old inode would
// stop protecting anything the moment someone saved, which is precisely when it
// matters. The lock file itself is never replaced.
func withLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}

// sessionLock guards one session's meta.json.
func sessionLock(id string) (string, error) {
	d, err := Dir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "meta.lock"), nil
}

// repoLock guards the "which session is current" decision for one repository.
// Attaching cannot use the session lock: the session may not exist yet, and two
// hooks racing to create one is exactly the case being prevented.
func repoLock(r Repo) (string, error) {
	p, err := pointerPath(r)
	if err != nil {
		return "", err
	}
	return p + ".lock", nil
}
