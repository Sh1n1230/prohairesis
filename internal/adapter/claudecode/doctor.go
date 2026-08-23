package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Sh1n1230/prohairesis/internal/diag"
)

// This file holds everything doctor knows about one particular agent runtime.
// It lives here rather than in the command for the same reason the rest of this
// package does: the command should be able to gain a second adapter without
// learning anything new about the first.

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

// Version reports the runtime's version, or "" if it is not on PATH.
func Version() string {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type settings struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
		Ask   []string `json:"ask"`
	} `json:"permissions"`
	Hooks   map[string]any `json:"hooks"`
	Sandbox map[string]any `json:"sandbox"`
}

// Inspect reports on this runtime's configuration. It reports; it changes
// nothing.
func Inspect(r *diag.Report, repoRoot string) {
	if v := Version(); v != "" {
		diag.Line(diag.Info, "detected "+v, "")
		diag.Line(diag.Info, "capability grade C1-C6 (pre-execution interception is advisory)", "")
	} else {
		diag.Line(diag.Info, "not detected on PATH", "")
	}
	inspectHooks(r, repoRoot)
	inspectSettings(r)
}

// inspectHooks says which scopes are recording.
//
// It says so even when the answer is "none", and it names the limit of a project
// scope out loud. A record that quietly covers one repository out of ten is
// worse than no record at all, because its silence looks like evidence.
func inspectHooks(r *diag.Report, repoRoot string) {
	userOn, _ := Installed(ScopeUser, "")
	projectOn := false
	if repoRoot != "" {
		projectOn, _ = Installed(ScopeProject, repoRoot)
	}
	userPath, _ := SettingsPath(ScopeUser, "")

	switch {
	case userOn && projectOn:
		diag.Line(diag.Info, "recording: everywhere (user scope)", userPath)
		r.Add(diag.Low,
			"hooks are registered in both scopes, so every tool call is recorded twice",
			"remove the trial: prohairesis hooks uninstall --scope project")
	case userOn:
		diag.Line(diag.Info, "recording: everywhere (user scope)", userPath)
	case projectOn:
		p, _ := SettingsPath(ScopeProject, repoRoot)
		diag.Line(diag.Info, "recording: this repository only (trial)", p)
		diag.Line(diag.Info, "work in other repositories is not observed", "")
		diag.Line(diag.Info, "make it permanent: prohairesis hooks install --scope user", "")
	default:
		r.Add(diag.Medium,
			"no hooks are registered, so nothing is being recorded and "+
				"checkpoints happen only when you ask for one",
			"run: prohairesis hooks install")
	}
}

func inspectSettings(r *diag.Report) {
	path, err := SettingsPath(ScopeUser, "")
	if err != nil {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		diag.Line(diag.Info, "no user settings file", path)
		return
	}
	var s settings
	if err := json.Unmarshal(b, &s); err != nil {
		r.Add(diag.Medium, "user settings file is not valid JSON", path)
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
		r.Add(diag.High, fmt.Sprintf(
			"%d permission rule(s) name a tool that does not exist, so they match nothing "+
				"and have never taken effect: %s", len(dead), strings.Join(dead, ", ")),
			path)
	} else {
		diag.Line(diag.Info, fmt.Sprintf("%d permission rules, all naming real tools",
			len(s.Permissions.Allow)+len(s.Permissions.Deny)+len(s.Permissions.Ask)), "")
	}

	// 2. A substring denylist over shell strings. Measured, not assumed:
	//    see tests/golden/false-block-corpus.jsonl.
	hookDir := filepath.Join(filepath.Dir(path), "hooks")
	if entries, err := os.ReadDir(hookDir); err == nil {
		for _, e := range entries {
			hb, err := os.ReadFile(filepath.Join(hookDir, e.Name()))
			if err != nil {
				continue
			}
			if strings.Contains(string(hb), "permissionDecision") &&
				strings.Contains(string(hb), "grep -Eq") {
				r.Add(diag.Medium,
					"a hook denies shell commands by matching substrings of the command line; "+
						"measured on this machine it refused 14 commands, 13 of which were harmless",
					filepath.Join(hookDir, e.Name()))
			}
		}
	}

	// 3. The runtime supports a kernel sandbox. Not configuring one leaves the
	//    only real enforcement mechanism switched off.
	if len(s.Sandbox) == 0 {
		r.Add(diag.Medium,
			"no sandbox configured, so nothing here is kernel-enforced; "+
				"reversibility is currently the only real protection",
			path)
	}

	// 4. The machine-level ceiling.
	managed := "/Library/Application Support/ClaudeCode/managed-settings.json"
	if _, err := os.Stat(managed); os.IsNotExist(err) {
		diag.Line(diag.Info, "no machine ceiling installed; repository config has full authority", managed)
	} else {
		diag.Line(diag.Info, "machine ceiling present", managed)
	}
}
