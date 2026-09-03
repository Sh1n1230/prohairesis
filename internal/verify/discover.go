// Package verify is the verification contract: the layer that answers "did this
// pass?" by running what the repository already declared, and normalizing the
// answer into one shape.
//
// The rules it lives under are narrow, and every tempting feature breaks one:
//
//   - It holds no definition of correctness. Every check it runs was declared by
//     the repository before this program existed. A check invented here would
//     make prohairesis an opinion about your project rather than infrastructure
//     underneath it.
//   - It is not a gate. It returns an exit code and a document. It refuses
//     nothing, blocks nothing, and has no verdict beyond the one the repository's
//     own tools produced.
//   - A missing tool is a skip, never an error. A machine without mypy is not a
//     machine with a failing project.
//
// The normalized shape is deliberately the one `~/security-checker` already
// emits, so that an agent has a single output shape to learn no matter which
// check produced it.
package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Check is one thing this repository declared as a way of checking itself.
type Check struct {
	// Category names the check in the normalized output. It is stable across
	// runs, because recurrence detection keys on it.
	Category string `json:"category"`
	// Source is the repository-relative file that declared it. Without it, a
	// reader has no way to argue with what was discovered.
	Source string `json:"source"`
	Argv   []string
}

// maxManifest bounds how much of a declaration file is read. A Makefile larger
// than this is not a Makefile this program should be parsing.
const maxManifest = 1 << 20

// Discover finds the checks a repository declares about itself.
//
// It is a ladder, not a union, and the order is the point. A repository that
// ships an aggregate quality script has already answered "how do I check
// myself"; running its Makefile as well would run the same tools twice and
// report the same failure twice, which is how a fingerprint stops meaning
// anything. Only the manifest tier collects several sources at once, because a
// polyglot repository's Cargo.toml genuinely does not claim to cover its
// package.json.
//
// Nothing here searches. Every path is a fixed, conventional location, so what
// this function will run is something a human can predict before running it.
func Discover(root string) []Check {
	if c := aggregate(root); len(c) > 0 {
		return c
	}
	if c := makefile(root); len(c) > 0 {
		return c
	}
	return manifests(root)
}

// aggregateScripts are the conventional names for "the script that runs
// everything this project considers a check".
var aggregateScripts = []string{
	"scripts/run_quality_checks.sh",
	"run_quality_checks.sh",
}

func aggregate(root string) []Check {
	for _, rel := range aggregateScripts {
		if !isFile(filepath.Join(root, rel)) {
			continue
		}
		// Run through bash rather than executing the file directly: a script
		// checked out without its executable bit is a permission accident, not a
		// statement that the repository has no checks.
		return []Check{{
			Category: "quality",
			Source:   rel,
			Argv:     []string{"bash", rel},
		}}
	}
	return nil
}

// targets are the target and script names that conventionally mean "check this
// project". The list is closed on purpose: discovering a target called `deploy`
// and running it would be this layer causing effects instead of observing them.
var targets = []string{"check", "verify", "lint", "typecheck", "test"}

var makeTarget = regexp.MustCompile(`(?m)^([A-Za-z0-9_.\-/]+)[ \t]*:(?:[^=]|$)`)

func makefile(root string) []Check {
	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		b, ok := readCapped(filepath.Join(root, name))
		if !ok {
			continue
		}
		declared := map[string]bool{}
		for _, m := range makeTarget.FindAllStringSubmatch(string(b), -1) {
			declared[m[1]] = true
		}
		var out []Check
		for _, t := range targets {
			if declared[t] {
				out = append(out, Check{
					Category: "make:" + t,
					Source:   name,
					Argv:     []string{"make", t},
				})
			}
		}
		return out
	}
	return nil
}

// manifests reads the declarations a language's own tooling understands. Unlike
// the tiers above, these are collected together: a repository with both a
// go.mod and a package.json has two halves, and checking one is not checking
// the other.
func manifests(root string) []Check {
	var out []Check
	out = append(out, node(root)...)
	out = append(out, cargo(root)...)
	out = append(out, golang(root)...)
	return out
}

// runners maps a lockfile to the package manager that wrote it. The lockfile is
// evidence; the absence of one is not, so npm is the fallback rather than a
// guess dressed up as a detection.
var runners = []struct{ lockfile, runner string }{
	{"pnpm-lock.yaml", "pnpm"},
	{"yarn.lock", "yarn"},
	{"bun.lockb", "bun"},
}

func node(root string) []Check {
	b, ok := readCapped(filepath.Join(root, "package.json"))
	if !ok {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return nil
	}
	runner := "npm"
	for _, r := range runners {
		if isFile(filepath.Join(root, r.lockfile)) {
			runner = r.runner
			break
		}
	}
	var out []Check
	for _, t := range targets {
		if strings.TrimSpace(pkg.Scripts[t]) == "" {
			continue
		}
		out = append(out, Check{
			Category: runner + ":" + t,
			Source:   "package.json",
			Argv:     []string{runner, "run", t},
		})
	}
	return out
}

func cargo(root string) []Check {
	if !isFile(filepath.Join(root, "Cargo.toml")) {
		return nil
	}
	return []Check{{
		Category: "cargo:test",
		Source:   "Cargo.toml",
		Argv:     []string{"cargo", "test"},
	}}
}

// golang is the same idea as cargo: the module file is the declaration, and the
// toolchain's own two verbs are what it declares. `gofmt` is deliberately absent
// -- formatting is a convention a repository may or may not hold, and go.mod
// does not declare it.
func golang(root string) []Check {
	if !isFile(filepath.Join(root, "go.mod")) {
		return nil
	}
	return []Check{
		{Category: "go:vet", Source: "go.mod", Argv: []string{"go", "vet", "./..."}},
		{Category: "go:test", Source: "go.mod", Argv: []string{"go", "test", "./..."}},
	}
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

func readCapped(p string) ([]byte, bool) {
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxManifest {
		return nil, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return b, true
}
