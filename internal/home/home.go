// Package home resolves the machine-level prohairesis state directory.
//
// Everything this project persists lives under one directory outside every
// repository. That is what makes uninstall complete and makes it impossible for
// an uninstall to corrupt a repository.
package home

import (
	"os"
	"path/filepath"
)

// EnvVar overrides the state directory. Tests set it; installs generally do not.
const EnvVar = "PROHAIRESIS_HOME"

// Dir returns the machine-level state directory, creating nothing.
func Dir() (string, error) {
	if d := os.Getenv(EnvVar); d != "" {
		return filepath.Abs(d)
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".prohairesis"), nil
}

// Sub returns a path under the state directory and ensures its parent exists.
func Sub(parts ...string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(append([]string{d}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// SubDir returns a directory under the state directory, creating it.
func SubDir(parts ...string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(append([]string{d}, parts...)...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}
