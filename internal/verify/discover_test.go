package verify

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func categories(cs []Check) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Category
	}
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The ladder is the reason a fingerprint means anything. A repository that ships
// an aggregate script and a Makefile that calls the same tools would otherwise
// report every failure twice, and "the same failure three times" would stop
// being a countable thing.
func TestTheMostSpecificDeclarationWins(t *testing.T) {
	root := t.TempDir()
	write(t, root, "scripts/run_quality_checks.sh", "#!/bin/sh\nexit 0\n")
	write(t, root, "Makefile", "test:\n\techo hi\n")
	write(t, root, "package.json", `{"scripts":{"test":"jest"}}`)

	if got := categories(Discover(root)); !same(got, []string{"quality"}) {
		t.Fatalf("discovered %v, want only the aggregate script", got)
	}
}

// Discovery decides what this program will execute. A target named `deploy` is
// exactly the thing a verification layer must never find interesting.
func TestOnlyCheckingTargetsAreDiscovered(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Makefile", ".PHONY: all\nall: build\nbuild:\n\tgo build\ndeploy:\n\t./ship.sh\nrelease: build\n\t./ship.sh\ntest:\n\tgo test\nlint:\n\truff check .\n")

	got := categories(Discover(root))
	if !same(got, []string{"make:lint", "make:test"}) {
		t.Fatalf("discovered %v, want only the checking targets in a fixed order", got)
	}
}

// A polyglot repository's two halves do not claim to cover each other, so the
// manifest tier is the one place discovery collects rather than choosing.
func TestManifestsAreCollectedTogether(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/x\n\ngo 1.27.0\n")
	write(t, root, "package.json", `{"scripts":{"lint":"eslint .","deploy":"./ship.sh"}}`)

	got := categories(Discover(root))
	if !same(got, []string{"npm:lint", "go:vet", "go:test"}) {
		t.Fatalf("discovered %v, want both halves and no deploy script", got)
	}
}

// The lockfile is evidence of which package manager this project uses. Guessing
// npm where a pnpm lockfile exists would run a command the repository never
// declared.
func TestTheLockfileChoosesTheRunner(t *testing.T) {
	for lockfile, want := range map[string]string{
		"pnpm-lock.yaml":     "pnpm:test",
		"yarn.lock":          "yarn:test",
		"package-lock.json":  "npm:test",
		"no-lockfile-at-all": "npm:test",
	} {
		root := t.TempDir()
		write(t, root, "package.json", `{"scripts":{"test":"vitest"}}`)
		write(t, root, lockfile, "")
		if got := categories(Discover(root)); !same(got, []string{want}) {
			t.Errorf("with %s discovered %v, want %s", lockfile, got, want)
		}
	}
}

func TestARepositoryThatDeclaresNothingDiscoversNothing(t *testing.T) {
	if got := Discover(t.TempDir()); len(got) != 0 {
		t.Fatalf("discovered %v in an empty repository; this layer invents no checks", categories(got))
	}
}

// A malformed manifest is not a failing project, and it is not a reason to guess
// either.
func TestAnUnreadableManifestIsNotAGuess(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", "{ this is not JSON")
	if got := Discover(root); len(got) != 0 {
		t.Fatalf("discovered %v from an unparseable manifest", categories(got))
	}
}
