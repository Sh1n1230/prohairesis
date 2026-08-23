package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/Sh1n1230/prohairesis/internal/gitx"
	"github.com/Sh1n1230/prohairesis/internal/home"
	"github.com/Sh1n1230/prohairesis/internal/session"
)

// A finding follows the same normalized shape the verification contract uses, so
// that everything this project reports can be read the same way.
type finding struct {
	severity string // CRITICAL HIGH MEDIUM LOW INFO
	message  string
	location string
}

type report struct {
	findings []finding
}

func (r *report) add(sev, msg, loc string) {
	r.findings = append(r.findings, finding{sev, msg, loc})
}

func (r *report) worst() string {
	rank := map[string]int{"CRITICAL": 4, "HIGH": 3, "MEDIUM": 2, "LOW": 1, "INFO": 0}
	w := "INFO"
	for _, f := range r.findings {
		if rank[f.severity] > rank[w] {
			w = f.severity
		}
	}
	return w
}

// toolsThatExist is the set of tool names a permission rule may legitimately
// name. A rule naming anything else matches nothing and is silently inert --
// the exact failure mode that motivated this project.
var toolsThatExist = map[string]bool{
	"Bash": true, "BashOutput": true, "KillShell": true,
	"Read": true, "Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true,
	"Glob": true, "Grep": true, "WebFetch": true, "WebSearch": true,
	"Task": true, "Agent": true, "TodoWrite": true, "Skill": true, "SlashCommand": true,
	"ExitPlanMode": true, "ListMcpResources": true, "ReadMcpResource": true,
}

var ruleRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\(`)

func line(sev, msg, loc string) {
	mark := map[string]string{
		"CRITICAL": "!!", "HIGH": " !", "MEDIUM": " ~", "LOW": " -", "INFO": " .",
	}[sev]
	fmt.Printf("%s %s\n", mark, msg)
	if loc != "" {
		fmt.Printf("     %s\n", loc)
	}
}

func cmdDoctor() int {
	r := &report{}

	fmt.Println("prohairesis doctor")
	fmt.Println()

	// --- environment ---------------------------------------------------------
	fmt.Println("environment")
	if v, ok := gitx.Available(); ok {
		line("INFO", "git "+v, "")
	} else {
		r.add("CRITICAL", "git not found; the reversibility layer cannot work", "")
	}
	if hd, err := home.Dir(); err == nil {
		st := "present"
		if _, err := os.Stat(hd); os.IsNotExist(err) {
			st = "not created yet"
		}
		line("INFO", "state directory "+st, hd)
	}
	if runtime.GOOS == "darwin" {
		if fsPersonality() == "APFS" {
			line("INFO", "APFS: copy-on-write snapshots are cheap here", "")
		} else {
			r.add("LOW", "root filesystem is not APFS; snapshots will copy rather than clone", "")
		}
	}

	// --- session -------------------------------------------------------------
	fmt.Println()
	fmt.Println("session")
	if s, err := session.Current(cwd()); err == nil {
		line("INFO", fmt.Sprintf("active: %s (%d checkpoints)", s.ID, len(s.Checkpoints)), s.Repo.Root)
	} else if _, ok := gitx.RepoRoot(cwd()); !ok {
		line("INFO", "not inside a git working tree", cwd())
	} else {
		line("MEDIUM", "no active session here; nothing is being checkpointed", "run: prohairesis session start")
		r.add("MEDIUM", "no active session here; nothing is being checkpointed",
			"run: prohairesis session start")
	}

	// --- adapter: claude code ------------------------------------------------
	fmt.Println()
	fmt.Println("adapter: claude-code")
	if v := claudeVersion(); v != "" {
		line("INFO", "detected "+v, "")
		line("INFO", "capability grade C1-C6 (pre-execution interception is advisory)", "")
	} else {
		line("INFO", "not detected on PATH", "")
	}
	checkClaudeSettings(r)

	// --- summary -------------------------------------------------------------
	fmt.Println()
	if len(r.findings) == 0 {
		fmt.Println("no findings")
		return 0
	}
	fmt.Printf("findings (%d)\n", len(r.findings))
	for _, f := range r.findings {
		line(f.severity, f.message, f.location)
	}
	fmt.Println()
	switch r.worst() {
	case "CRITICAL", "HIGH":
		return 1
	}
	return 0
}

func fsPersonality() string {
	out, err := exec.Command("diskutil", "info", "/").Output()
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.Contains(ln, "File System Personality") {
			if i := strings.Index(ln, ":"); i >= 0 {
				return strings.TrimSpace(ln[i+1:])
			}
		}
	}
	return ""
}

func claudeVersion() string {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type claudeSettings struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
		Ask   []string `json:"ask"`
	} `json:"permissions"`
	Hooks   map[string]any `json:"hooks"`
	Sandbox map[string]any `json:"sandbox"`
}

// checkClaudeSettings looks for the specific, verified failure modes this project
// exists to fix. It reports; it changes nothing.
func checkClaudeSettings(r *report) {
	hd, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(hd, ".claude", "settings.json")
	b, err := os.ReadFile(path)
	if err != nil {
		line("INFO", "no user settings file", path)
		return
	}
	var s claudeSettings
	if err := json.Unmarshal(b, &s); err != nil {
		r.add("MEDIUM", "user settings file is not valid JSON", path)
		return
	}

	// 1. Permission rules naming a tool that does not exist match nothing.
	var dead []string
	for _, group := range [][]string{s.Permissions.Allow, s.Permissions.Deny, s.Permissions.Ask} {
		for _, rule := range group {
			m := ruleRe.FindStringSubmatch(rule)
			if m == nil {
				continue
			}
			if name := m[1]; !toolsThatExist[name] && !strings.HasPrefix(name, "mcp__") {
				dead = append(dead, rule)
			}
		}
	}
	if len(dead) > 0 {
		r.add("HIGH", fmt.Sprintf(
			"%d permission rule(s) name a tool that does not exist, so they match nothing "+
				"and have never taken effect: %s", len(dead), strings.Join(dead, ", ")),
			path)
	} else {
		line("INFO", fmt.Sprintf("%d permission rules, all naming real tools",
			len(s.Permissions.Allow)+len(s.Permissions.Deny)+len(s.Permissions.Ask)), "")
	}

	// 2. A substring denylist over shell strings. Measured, not assumed:
	//    see tests/golden/false-block-corpus.jsonl.
	hookDir := filepath.Join(hd, ".claude", "hooks")
	if entries, err := os.ReadDir(hookDir); err == nil {
		for _, e := range entries {
			hb, err := os.ReadFile(filepath.Join(hookDir, e.Name()))
			if err != nil {
				continue
			}
			if strings.Contains(string(hb), "permissionDecision") &&
				strings.Contains(string(hb), "grep -Eq") {
				r.add("MEDIUM",
					"a hook denies shell commands by matching substrings of the command line; "+
						"measured on this machine it refused 14 commands, 13 of which were harmless",
					filepath.Join(hookDir, e.Name()))
			}
		}
	}

	// 3. The runtime supports a kernel sandbox. Not configuring one leaves the
	//    only real enforcement mechanism switched off.
	if len(s.Sandbox) == 0 {
		r.add("MEDIUM",
			"no sandbox configured, so nothing here is kernel-enforced; "+
				"reversibility is currently the only real protection",
			path)
	}

	// 4. The machine-level ceiling.
	managed := "/Library/Application Support/ClaudeCode/managed-settings.json"
	if _, err := os.Stat(managed); os.IsNotExist(err) {
		line("INFO", "no machine ceiling installed; repository config has full authority", managed)
	} else {
		line("INFO", "machine ceiling present", managed)
	}
}
