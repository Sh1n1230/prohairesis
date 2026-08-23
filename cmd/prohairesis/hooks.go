package main

import (
	"fmt"
	"os"

	"github.com/Sh1n1230/prohairesis/internal/adapter/claudecode"
	"github.com/Sh1n1230/prohairesis/internal/session"
)

// cmdHooks registers this program with an agent runtime, or removes it.
//
// It is a command of its own rather than a step of the installer on purpose. The
// installer's promise is that it puts a binary somewhere and touches nothing
// else -- no shell profile, no configuration. Quietly rewriting the file that
// decides how your agent behaves would break that promise from the inside, and
// this project has no standing to ask for trust it will not extend.
func cmdHooks(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("hooks: expected install|uninstall")
	}
	rest := args[1:]
	scopeArg, rest := flag(rest, "scope")
	rest, dry := has(rest, "dry-run")

	// The project scope is the default because it is the reversible one: it
	// changes a single repository, in a file its owner already reads, and
	// undoing it is deleting a file. The user scope is where this belongs once
	// it has earned staying -- installed once, in effect everywhere -- and is
	// asked for explicitly rather than arrived at by default.
	scope := claudecode.ScopeProject
	switch scopeArg {
	case "", "project":
	case "user":
		scope = claudecode.ScopeUser
	default:
		return fmt.Errorf("hooks: unknown scope %q; expected project or user", scopeArg)
	}

	root := ""
	if repo, err := session.DescribeRepo(cwd()); err == nil {
		root = repo.Root
	} else if scope == claudecode.ScopeProject {
		return err
	}

	var (
		plan claudecode.Plan
		err  error
		verb string
	)
	switch args[0] {
	case "install":
		bin, e := os.Executable()
		if e != nil {
			return e
		}
		plan, err = claudecode.Install(scope, root, bin)
		verb = "install"
	case "uninstall":
		plan, err = claudecode.Uninstall(scope, root)
		verb = "uninstall"
	default:
		return fmt.Errorf("hooks: unknown subcommand %q", args[0])
	}
	if err != nil {
		return err
	}

	fmt.Printf("%s (%s scope)\n", plan.Path, scope)
	if !plan.Changed {
		fmt.Printf("  already %sed; nothing to do\n", verb)
		return nil
	}
	if dry {
		fmt.Println()
		fmt.Print(string(plan.After))
		fmt.Println("\n(dry run; nothing changed)")
		return nil
	}
	if err := plan.Write(); err != nil {
		return err
	}

	if verb == "install" {
		fmt.Println("  recording session start, session end and every tool call")
		fmt.Println("  nothing here can block a tool call; every hook exits 0")
		if scope == claudecode.ScopeProject {
			fmt.Println()
			fmt.Println("  This covers one repository. Work in any other repository is not")
			fmt.Println("  observed, and metrics will say which repositories they cover.")
			fmt.Println("  When you want it everywhere: prohairesis hooks install --scope user")
			fmt.Println("  (then remove the trial: prohairesis hooks uninstall --scope project)")
		}
		return nil
	}
	fmt.Println("  removed; the file is back to what it was before")
	return nil
}
