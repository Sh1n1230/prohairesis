package adapter_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The seam this project claims to have is that one agent runtime is known to one
// package and to nothing else. That claim is worth exactly as much as the check
// behind it: an abstraction nobody verifies has usually already leaked, and the
// leak is found years later by the second implementation that was supposed to
// prove the abstraction worked.
//
// So the check runs now, with one adapter, when it can still be cheap to fix.
//
// It is not a test of naming hygiene. It is the test that decides whether the
// canonical action vocabulary is real or decorative.
func TestOnlyTheAdapterKnowsTheRuntime(t *testing.T) {
	root := filepath.Join("..", "..")
	exempt := map[string]bool{
		filepath.Join("internal", "adapter", "claudecode"): true,
	}

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if info.IsDir() {
			switch {
			case exempt[rel]:
				return filepath.SkipDir
			case rel == ".":
				return nil
			case strings.HasPrefix(rel, "internal"), strings.HasPrefix(rel, "cmd"):
				return nil
			default:
				return filepath.SkipDir
			}
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if rel == filepath.Join("internal", "adapter", "seam_test.go") {
			// The check has to name what it forbids.
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			lower := strings.ToLower(line)
			if !strings.Contains(lower, "claude") {
				continue
			}
			// An import of the adapter, and the package name it binds, are how
			// the seam is crossed on purpose. Everything else is a leak.
			if strings.Contains(line, "internal/adapter/claudecode") ||
				strings.Contains(line, "claudecode.") {
				continue
			}
			offenders = append(offenders, rel+":"+itoa(i+1)+": "+strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("the runtime is named outside its adapter, so the canonical action "+
			"vocabulary is not carrying what it claims to:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
