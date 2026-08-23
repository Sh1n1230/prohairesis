package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Sh1n1230/prohairesis/internal/adapter/claudecode"
	"github.com/Sh1n1230/prohairesis/internal/diag"
	"github.com/Sh1n1230/prohairesis/internal/event"
	"github.com/Sh1n1230/prohairesis/internal/gitx"
	"github.com/Sh1n1230/prohairesis/internal/home"
	"github.com/Sh1n1230/prohairesis/internal/session"
)

func cmdDoctor() int {
	r := &diag.Report{}

	fmt.Println("prohairesis doctor")
	fmt.Println()

	// --- environment ---------------------------------------------------------
	fmt.Println("environment")
	if v, ok := gitx.Available(); ok {
		diag.Line(diag.Info, "git "+v, "")
	} else {
		r.Add(diag.Critical, "git not found; the reversibility layer cannot work", "")
	}
	if hd, err := home.Dir(); err == nil {
		st := "present"
		if _, err := os.Stat(hd); os.IsNotExist(err) {
			st = "not created yet"
		}
		diag.Line(diag.Info, "state directory "+st, hd)
	}
	if runtime.GOOS == "darwin" {
		if fsPersonality() == "APFS" {
			diag.Line(diag.Info, "APFS: copy-on-write snapshots are cheap here", "")
		} else {
			r.Add(diag.Low, "root filesystem is not APFS; snapshots will copy rather than clone", "")
		}
	}

	// --- session -------------------------------------------------------------
	fmt.Println()
	fmt.Println("session")
	repoRoot := ""
	if repo, err := session.DescribeRepo(cwd()); err == nil {
		repoRoot = repo.Root
	}
	if s, err := session.Current(cwd()); err == nil {
		who := "started by hand"
		if s.OwnedByHook() {
			who = "opened by the hook"
		}
		diag.Line(diag.Info, fmt.Sprintf("active: %s (%d checkpoints, %s, %d attached)",
			s.ID, len(s.Checkpoints), who, s.AttachedCount()), s.Repo.Root)
	} else if repoRoot == "" {
		diag.Line(diag.Info, "not inside a git working tree", cwd())
	} else {
		r.Add(diag.Medium, "no active session here; nothing is being checkpointed",
			"run: prohairesis session start, or register the hooks")
	}

	// --- observation ---------------------------------------------------------
	fmt.Println()
	fmt.Println("observation")
	lost, lastLoss, err := event.LossCount()
	switch {
	case err != nil:
		diag.Line(diag.Info, "cannot read the loss record", "")
	case lost == 0:
		diag.Line(diag.Info, "no observations are known to have been lost", "")
	default:
		// A hole in the record is not a small thing: everything downstream
		// treats silence as "nothing happened", and here it does not mean that.
		r.Add(diag.Medium, fmt.Sprintf(
			"%d observation(s) could not be written; the event log has holes and its "+
				"silence does not mean nothing happened (most recent %s)", lost, age(lastLoss)),
			lossPath())
	}

	// --- adapter -------------------------------------------------------------
	fmt.Println()
	fmt.Println("adapter: " + claudecode.Name)
	claudecode.Inspect(r, repoRoot)

	// --- summary -------------------------------------------------------------
	fmt.Println()
	if len(r.Findings) == 0 {
		fmt.Println("no findings")
		return 0
	}
	fmt.Printf("findings (%d)\n", len(r.Findings))
	for _, f := range r.Findings {
		f.Print()
	}
	fmt.Println()
	if r.Failed() {
		return 1
	}
	return 0
}

func lossPath() string {
	d, err := home.Dir()
	if err != nil {
		return ""
	}
	return d + "/" + event.LossFile
}

// installedFor reports whether either scope is recording for a repository.
func installedFor(repoRoot string) (bool, error) {
	if ok, err := claudecode.Installed(claudecode.ScopeUser, ""); err != nil || ok {
		return ok, err
	}
	if repoRoot == "" {
		return false, nil
	}
	return claudecode.Installed(claudecode.ScopeProject, repoRoot)
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
