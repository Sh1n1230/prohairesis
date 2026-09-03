// Command prohairesis is the machine-level agent infrastructure CLI.
//
// Exit codes are uniform across every subcommand:
//
//	0  clean
//	1  gate failed (a check did not pass; the command itself worked)
//	2  execution error
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Sh1n1230/prohairesis/internal/pathx"
	"github.com/Sh1n1230/prohairesis/internal/session"
	"github.com/Sh1n1230/prohairesis/internal/snapshot"
)

// version is stamped at release time with -ldflags "-X main.version=...".
// It must stay a var: the linker cannot write to a const, and would
// silently leave the placeholder in a published binary.
var version = "0.0.0-dev"

// usage embeds version, which is written at link time, so it cannot be a const.
var usage = `prohairesis ` + version + ` -- machine-level agent infrastructure

reversibility
  session start [--adapter NAME]   begin a session for the repository in the cwd
  session end                      close the current session
  session list                     list known sessions
  session show [ID]                show one session
  checkpoint [--label TEXT]        capture the working tree now
  diff [SEQ]                       what changed since a checkpoint (default: latest)
  undo [SEQ] [--dry-run]           restore the working tree to a checkpoint
  protect list|add PATH|rm PATH    declare an ignored path precious, so that
                                   checkpoints capture it too

verification
  verify [--json]                  run the checks this repository declares and
                                   report them in one normalized shape. It gates
                                   nothing: the exit code reports what your own
                                   tools said

observation
  hooks install [--scope project|user] [--dry-run]
  hooks uninstall [--scope ...]    register with the agent runtime, or stop.
                                   Recording only: nothing here can refuse a
                                   tool call. Project scope is the trial; user
                                   scope is install-once, in effect everywhere
  report [ID]                      what happened in one session
  metrics                          friction now against the baseline before this

diagnostics
  doctor                           report what is and is not in effect
  version

Exit codes: 0 clean, 1 gate failed, 2 error.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "session":
		err = cmdSession(args)
	case "checkpoint":
		err = cmdCheckpoint(args)
	case "diff":
		err = cmdDiff(args)
	case "undo":
		err = cmdUndo(args)
	case "protect":
		err = cmdProtect(args)
	case "verify":
		os.Exit(cmdVerify(args))
	case "hooks":
		err = cmdHooks(args)
	case "hook":
		// Called by the agent runtime, many times a session. It reports nothing
		// and fails at nothing: see cmdHook.
		os.Exit(cmdHook(args))
	case "report":
		err = cmdReport(args)
	case "metrics":
		err = cmdMetrics(args)
	case "doctor":
		os.Exit(cmdDoctor())
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "prohairesis: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "prohairesis: "+err.Error())
		os.Exit(2)
	}
}

func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

// flag pulls "--name value" or "--name=value" out of args.
func flag(args []string, name string) (string, []string) {
	out := make([]string, 0, len(args))
	val := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--"+name && i+1 < len(args):
			val = args[i+1]
			i++
		case strings.HasPrefix(a, "--"+name+"="):
			val = strings.TrimPrefix(a, "--"+name+"=")
		default:
			out = append(out, a)
		}
	}
	return val, out
}

func has(args []string, name string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for _, a := range args {
		if a == "--"+name {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

func cmdSession(args []string) error {
	if len(args) == 0 {
		return errors.New("session: expected start|end|list|show")
	}
	rest := args[1:]
	switch args[0] {
	case "start":
		adapter, _ := flag(rest, "adapter")
		if adapter == "" {
			adapter = "manual"
		}
		s, err := session.Start(cwd(), adapter)
		if err != nil {
			return err
		}
		rec, err := s.Checkpoint("session start")
		if err != nil {
			return fmt.Errorf("session started but initial checkpoint failed: %w", err)
		}
		fmt.Printf("session %s started (%s)\n", s.ID, s.Repo.Root)
		fmt.Printf("  checkpoint %d  %s\n", rec.Seq, rec.Commit[:12])
		return nil

	case "end":
		s, err := session.Current(cwd())
		if err != nil {
			return err
		}
		if _, err := s.Checkpoint("session end"); err != nil {
			return err
		}
		if err := s.End(); err != nil {
			return err
		}
		fmt.Printf("session %s ended (%d checkpoints)\n", s.ID, len(s.Checkpoints))
		return nil

	case "list":
		all, err := session.List()
		if err != nil {
			return err
		}
		if len(all) == 0 {
			fmt.Println("no sessions")
			return nil
		}
		for _, s := range all {
			state := "open"
			if s.Ended != nil {
				state = "ended"
			}
			fmt.Printf("%s  %-5s  %2d ckpt  %s\n",
				s.ID, state, len(s.Checkpoints), s.Repo.Root)
		}
		return nil

	case "show":
		s, err := resolveSession(rest)
		if err != nil {
			return err
		}
		fmt.Printf("session   %s\n", s.ID)
		fmt.Printf("adapter   %s\n", s.Adapter)
		fmt.Printf("repo      %s (%s)\n", s.Repo.Root, s.Repo.Branch)
		fmt.Printf("started   %s\n", s.Started.Format("2006-01-02 15:04:05Z"))
		if s.Ended != nil {
			fmt.Printf("ended     %s\n", s.Ended.Format("2006-01-02 15:04:05Z"))
		}
		fmt.Println("checkpoints")
		for _, c := range s.Checkpoints {
			fmt.Printf("  %2d  %s  %s\n", c.Seq, c.Commit[:12], c.Label)
		}
		return nil
	}
	return fmt.Errorf("session: unknown subcommand %q", args[0])
}

func resolveSession(args []string) (*session.Session, error) {
	if len(args) > 0 && args[0] != "" {
		return session.Load(args[0])
	}
	return session.Current(cwd())
}

func cmdCheckpoint(args []string) error {
	label, _ := flag(args, "label")
	s, err := session.Current(cwd())
	if err != nil {
		return err
	}
	rec, err := s.Checkpoint(label)
	if err != nil {
		return err
	}
	fmt.Printf("checkpoint %d  %s\n", rec.Seq, rec.Commit[:12])
	return nil
}

// pickCheckpoint resolves an optional sequence argument to a commit, defaulting
// to the most recent checkpoint.
func pickCheckpoint(s *session.Session, args []string) (int, string, error) {
	st, err := s.Store()
	if err != nil {
		return 0, "", err
	}
	list, err := st.List()
	if err != nil {
		return 0, "", err
	}
	if len(list) == 0 {
		return 0, "", errors.New("this session has no checkpoints")
	}
	seq := list[len(list)-1].Seq
	if len(args) > 0 && args[0] != "" {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return 0, "", fmt.Errorf("expected a checkpoint number, got %q", args[0])
		}
		seq = n
	}
	commit, err := st.Resolve(seq)
	return seq, commit, err
}

func cmdDiff(args []string) error {
	s, err := session.Current(cwd())
	if err != nil {
		return err
	}
	seq, commit, err := pickCheckpoint(s, args)
	if err != nil {
		return err
	}
	st, err := s.Store()
	if err != nil {
		return err
	}
	changes, err := st.Diff(commit)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Printf("no changes since checkpoint %d\n", seq)
		return nil
	}
	fmt.Printf("changes since checkpoint %d (%s)\n", seq, commit[:12])
	for _, c := range changes {
		fmt.Printf("  %s  %s\n", c.Status, c.Path)
	}
	return nil
}

func cmdUndo(args []string) error {
	args, dry := has(args, "dry-run")
	s, err := session.Current(cwd())
	if err != nil {
		return err
	}
	seq, commit, err := pickCheckpoint(s, args)
	if err != nil {
		return err
	}
	st, err := s.Store()
	if err != nil {
		return err
	}
	changes, err := st.Diff(commit)
	if err != nil {
		return err
	}

	fmt.Printf("restore working tree to checkpoint %d (%s)\n", seq, commit[:12])
	if len(changes) == 0 {
		fmt.Println("  nothing to do; the tree already matches")
		return nil
	}
	for _, c := range changes {
		// Status is relative to the checkpoint, so invert it for the human: a
		// path added since the checkpoint will be removed by the restore.
		verb := map[string]string{"A": "remove ", "D": "restore", "M": "revert "}[c.Status]
		if verb == "" {
			verb = c.Status + "      "
		}
		fmt.Printf("  %s  %s\n", verb, c.Path)
	}

	// Effects that left this machine cannot be undone. Say so before acting,
	// every time, so nobody mistakes a tree restore for a rollback.
	fmt.Println()
	fmt.Println("  not covered: anything that already left this machine --")
	fmt.Println("  pushed commits, sent messages, published artifacts, remote state.")
	fmt.Println("  Ignored paths are not touched.")

	if dry {
		fmt.Println("\n(dry run; nothing changed)")
		return nil
	}
	if err := st.Restore(commit); err != nil {
		return err
	}
	fmt.Printf("\nrestored %d path(s)\n", len(changes))

	precious, err := s.RestoreSnapshot(seq)
	if err != nil {
		return fmt.Errorf("tracked files restored, but precious paths did not: %w", err)
	}
	for _, p := range precious {
		fmt.Printf("  precious  %s\n", p)
	}
	return nil
}

func cmdProtect(args []string) error {
	repo, err := session.DescribeRepo(cwd())
	if err != nil {
		return err
	}
	cfg, err := snapshot.LoadConfig(repo.Key())
	if err != nil {
		return err
	}

	action := "list"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "list":
		if len(cfg.Paths) == 0 {
			fmt.Println("no precious paths declared for this repository")
			fmt.Println()
			fmt.Println("Declare paths git ignores but you cannot afford to lose, for example:")
			fmt.Println("  prohairesis protect add data/interim")
			fmt.Println()
			fmt.Println("Large directories should be protected by denying writes, not by copying")
			fmt.Println("them on every checkpoint. The ceiling is", cfg.MaxBytes>>20, "MiB.")
			return nil
		}
		for _, p := range cfg.Paths {
			fmt.Println(" ", p)
		}
		return nil

	case "add", "rm":
		if len(args) < 2 {
			return fmt.Errorf("protect %s: expected a path", action)
		}
		rel, err := relToRepo(repo.Root, args[1])
		if err != nil {
			return err
		}
		changed := false
		if action == "add" {
			changed = cfg.Add(rel)
		} else {
			changed = cfg.Remove(rel)
		}
		if !changed {
			fmt.Printf("%s: no change\n", rel)
			return nil
		}
		if err := cfg.Save(repo.Key()); err != nil {
			return err
		}
		fmt.Printf("%s %s\n", map[string]string{"add": "protecting", "rm": "unprotected"}[action], rel)
		return nil
	}
	return fmt.Errorf("protect: unknown subcommand %q", action)
}

func relToRepo(root, p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	// Resolve both sides: on macOS the temp and home directories are reached
	// through symlinks, so a raw string comparison reports a path inside the
	// repository as being outside it.
	rel, err := filepath.Rel(pathx.Resolve(root), pathx.Resolve(abs))
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s is outside the repository at %s", p, root)
	}
	return rel, nil
}
