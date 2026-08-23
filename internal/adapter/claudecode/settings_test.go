package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A settings file shaped like the ones this actually meets: several top-level
// keys in a deliberate order, and a hook someone else installed, carrying a
// field this program knows nothing about.
const realistic = `{
  "permissions": {
    "deny": [
      "Read(**/.env)"
    ]
  },
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "/Users/someone/.claude/hooks/block-secrets.sh",
            "statusMessage": "Checking command for secret access..."
          }
        ]
      }
    ]
  }
}
`

func writeSettings(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(EnvConfigDir, dir)
	path := filepath.Join(dir, "settings.json")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func install(t *testing.T) {
	t.Helper()
	p, err := Install(ScopeUser, "", "/usr/local/bin/prohairesis")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Write(); err != nil {
		t.Fatal(err)
	}
}

func uninstall(t *testing.T) {
	t.Helper()
	p, err := Uninstall(ScopeUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Write(); err != nil {
		t.Fatal(err)
	}
}

// Uninstall has to be an exact reversal. Anything less means the price of trying
// this is a settings file you can no longer diff against what you wrote.
func TestUninstallRestoresTheFileByteForByte(t *testing.T) {
	path := writeSettings(t, realistic)

	install(t)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), marker) {
		t.Fatalf("install did not register anything:\n%s", after)
	}
	if !strings.Contains(string(after), "block-secrets.sh") ||
		!strings.Contains(string(after), "statusMessage") {
		t.Fatalf("install disturbed a hook it did not own:\n%s", after)
	}

	uninstall(t)
	back, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != realistic {
		t.Fatalf("uninstall did not restore the file byte for byte.\nwant:\n%s\ngot:\n%s",
			realistic, back)
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	path := writeSettings(t, realistic)
	install(t)
	once, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	install(t)
	twice, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Fatalf("installing twice differs from installing once:\n%s\n---\n%s", once, twice)
	}
	ok, err := Installed(ScopeUser, "")
	if err != nil || !ok {
		t.Fatalf("Installed() = %v, %v; want true", ok, err)
	}
}

// A file this project created should not outlive it.
func TestUninstallLeavesNoFileBehindWhenItCreatedOne(t *testing.T) {
	path := writeSettings(t, "")
	install(t)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("install created no settings file: %v", err)
	}
	uninstall(t)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		b, _ := os.ReadFile(path)
		t.Fatalf("uninstall left a file behind:\n%s", b)
	}
}

func TestUninstallKeepsHooksItDoesNotOwn(t *testing.T) {
	path := writeSettings(t, realistic)
	install(t)
	uninstall(t)
	back, _ := os.ReadFile(path)
	if !strings.Contains(string(back), "block-secrets.sh") {
		t.Fatalf("uninstall removed a hook belonging to someone else:\n%s", back)
	}
	if ok, _ := Installed(ScopeUser, ""); ok {
		t.Fatal("Installed() still reports true after uninstall")
	}
}
