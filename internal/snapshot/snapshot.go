// Package snapshot covers what the git layer deliberately cannot: files a
// repository ignores.
//
// Ignored paths are where a working session keeps the things that are expensive
// or impossible to reproduce -- local environment files, intermediate data,
// build outputs. git will not capture them, by design, and the checkpoint layer
// honours that. This package captures the ones a human has explicitly declared
// precious, and only those.
//
// Two rules keep it from becoming a backup tool:
//
//   - Nothing is captured unless it was declared. There is no heuristic.
//   - There is a size ceiling. Large data directories are meant to be protected
//     by denying writes to them, not by copying them on every checkpoint.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/Sh1n1230/prohairesis/internal/home"
)

// DefaultMaxBytes is the ceiling for one snapshot. Above it, capture refuses and
// says so rather than quietly making every checkpoint expensive.
const DefaultMaxBytes int64 = 2 << 30 // 2 GiB

// Config is the per-repository declaration of what is precious.
type Config struct {
	Schema   string   `json:"schema"`
	Paths    []string `json:"paths"`
	MaxBytes int64    `json:"max_bytes"`
}

const schemaName = "harness.protect.v1"

func configPath(repoKey string) (string, error) {
	sum := sha256.Sum256([]byte(repoKey))
	return home.Sub("protect", hex.EncodeToString(sum[:8])+".json")
}

// LoadConfig returns the declaration for a repository, or an empty one.
func LoadConfig(repoKey string) (*Config, error) {
	p, err := configPath(repoKey)
	if err != nil {
		return nil, err
	}
	c := &Config{Schema: schemaName, MaxBytes: DefaultMaxBytes}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("protect list is corrupt: %w", err)
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = DefaultMaxBytes
	}
	return c, nil
}

// Save persists the declaration.
func (c *Config) Save(repoKey string) error {
	p, err := configPath(repoKey)
	if err != nil {
		return err
	}
	c.Schema = schemaName
	sort.Strings(c.Paths)
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

// Add declares a repository-relative path precious. It reports whether the set changed.
func (c *Config) Add(rel string) bool {
	rel = filepath.Clean(rel)
	for _, p := range c.Paths {
		if p == rel {
			return false
		}
	}
	c.Paths = append(c.Paths, rel)
	return true
}

// Remove undeclares a path.
func (c *Config) Remove(rel string) bool {
	rel = filepath.Clean(rel)
	out := c.Paths[:0]
	found := false
	for _, p := range c.Paths {
		if p == rel {
			found = true
			continue
		}
		out = append(out, p)
	}
	c.Paths = out
	return found
}

// Result describes one capture.
type Result struct {
	Paths   []string `json:"paths"`
	Bytes   int64    `json:"bytes"`
	Skipped []string `json:"skipped,omitempty"`
	Cloned  bool     `json:"cloned"`
}

// Capture copies every declared path into dst.
//
// On APFS this is a copy-on-write clone, so it costs almost nothing in time or
// space. Elsewhere it falls back to a reflink where the filesystem supports one,
// and to a plain copy otherwise.
func Capture(repoRoot, dst string, c *Config) (Result, error) {
	var res Result
	if len(c.Paths) == 0 {
		return res, nil
	}

	var total int64
	present := make([]string, 0, len(c.Paths))
	for _, rel := range c.Paths {
		src := filepath.Join(repoRoot, rel)
		n, err := sizeOf(src)
		if err != nil {
			res.Skipped = append(res.Skipped, rel+" (absent)")
			continue
		}
		total += n
		present = append(present, rel)
	}
	if total > c.MaxBytes {
		return res, fmt.Errorf(
			"declared precious paths total %s, above the %s ceiling; "+
				"protect a smaller set, or deny writes to the large ones instead of copying them",
			human(total), human(c.MaxBytes))
	}

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return res, err
	}
	for _, rel := range present {
		src := filepath.Join(repoRoot, rel)
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return res, err
		}
		cloned, err := copyTree(src, out)
		if err != nil {
			return res, fmt.Errorf("capture %s: %w", rel, err)
		}
		res.Cloned = res.Cloned || cloned
	}
	res.Paths = present
	res.Bytes = total
	return res, nil
}

// Restore puts captured paths back, replacing whatever is there now.
func Restore(repoRoot, src string, c *Config) ([]string, error) {
	var done []string
	for _, rel := range c.Paths {
		from := filepath.Join(src, rel)
		if _, err := os.Lstat(from); err != nil {
			continue // not in this checkpoint
		}
		to := filepath.Join(repoRoot, rel)
		if err := os.RemoveAll(to); err != nil {
			return done, fmt.Errorf("clear %s: %w", rel, err)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return done, err
		}
		if _, err := copyTree(from, to); err != nil {
			return done, fmt.Errorf("restore %s: %w", rel, err)
		}
		done = append(done, rel)
	}
	return done, nil
}

// copyTree prefers a copy-on-write clone. It reports whether one was used.
func copyTree(src, dst string) (bool, error) {
	if runtime.GOOS == "darwin" {
		// -c requests clonefile(2); it fails cleanly on non-APFS volumes.
		if err := exec.Command("cp", "-Rc", src, dst).Run(); err == nil {
			return true, nil
		}
	}
	if runtime.GOOS == "linux" {
		if err := exec.Command("cp", "-a", "--reflink=auto", src, dst).Run(); err == nil {
			return true, nil
		}
	}
	if err := exec.Command("cp", "-R", src, dst).Run(); err != nil {
		return false, err
	}
	return false, nil
}

func sizeOf(path string) (int64, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !fi.IsDir() {
		return fi.Size(), nil
	}
	var total int64
	err = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry must not fail sizing
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func human(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return strings.TrimSpace(fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp]))
}
