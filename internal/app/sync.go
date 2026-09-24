package app

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// syncIgnore keeps machine-local and binary state out of git: the SQLite
// files (use `focusflow backup` for those), backups, caches, logs, and
// each task's workspace (user work files, not notes).
const syncIgnore = `# managed by focusflow sync
focusflow.db
focusflow.db-*
backups/
stats-cache.json
stats-card.html
*.log
tasks/*/workspace/
tasks/*/auto-runs/
relocated/
`

// runGit runs git in dir and returns combined output. Identity is supplied
// per-command so sync works on a machine with no global git config
// without ever writing to the user's git config.
func runGit(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir, "-c", "user.name=focusflow", "-c", "user.email=focusflow@localhost"}, args...)
	cmd := exec.Command("git", full...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func isGitRepo(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

func cmdSync(args []string) int {
	sub := ""
	if len(args) > 0 && !looksLikeFlag(args[0]) {
		sub, args = args[0], args[1:]
	}
	root, err := dataRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if _, err := exec.LookPath("git"); err != nil {
		fmt.Fprintln(os.Stderr, "error: git is not installed")
		return 1
	}
	switch sub {
	case "init":
		return syncInit(root, args)
	case "status":
		return syncStatus(root)
	case "", "push":
		return syncRun(root, args)
	}
	fmt.Fprintf(os.Stderr, "error: unknown sync subcommand %q (init|status|push)\n", sub)
	return 2
}

func syncInit(root string, args []string) int {
	fs := flagSet("sync init")
	remote := fs.String("remote", "", "git remote URL to push to (a PRIVATE repository — your briefs and KB are personal)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if isGitRepo(root) {
		fmt.Println("already a git repository")
	} else {
		if out, err := runGit(root, "init", "-b", "main"); err != nil {
			fmt.Fprintf(os.Stderr, "error: git init: %v: %s\n", err, out)
			return 1
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(syncIgnore), privFile); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if *remote != "" {
		if _, err := runGit(root, "remote", "get-url", "origin"); err == nil {
			runGit(root, "remote", "set-url", "origin", *remote)
		} else if out, err := runGit(root, "remote", "add", "origin", *remote); err != nil {
			fmt.Fprintf(os.Stderr, "error: git remote add: %v: %s\n", err, out)
			return 1
		}
		fmt.Printf("remote origin → %s (make sure it is a private repository)\n", *remote)
	}
	fmt.Println("initialized. Tracked: briefs, updates, notes, KB. Not tracked: the database (use `focusflow backup`).")
	return syncRun(root, nil)
}

func syncStatus(root string) int {
	if !isGitRepo(root) {
		fmt.Println("not initialized — run `focusflow sync init [--remote <url>]`")
		return 1
	}
	out, _ := runGit(root, "status", "--short")
	if out == "" {
		fmt.Println("clean — nothing to sync")
	} else {
		fmt.Println(out)
	}
	if r, err := runGit(root, "remote", "get-url", "origin"); err == nil {
		fmt.Printf("remote: %s\n", r)
	} else {
		fmt.Println("remote: (none — commits stay local)")
	}
	return 0
}

// syncRun commits any changes, then (when a remote is configured) rebases
// on the remote and pushes. It never force-pushes and never runs unless
// the user invoked it.
func syncRun(root string, args []string) int {
	fs := flagSet("sync")
	msg := fs.String("message", "", "commit message (default: timestamp)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !isGitRepo(root) {
		fmt.Fprintln(os.Stderr, "error: not initialized — run `focusflow sync init [--remote <url>]`")
		return 1
	}
	if out, err := runGit(root, "add", "-A"); err != nil {
		fmt.Fprintf(os.Stderr, "error: git add: %v: %s\n", err, out)
		return 1
	}
	if st, _ := runGit(root, "status", "--porcelain"); st != "" {
		m := *msg
		if m == "" {
			m = "focusflow sync " + time.Now().Format(time.RFC3339)
		}
		if out, err := runGit(root, "commit", "-m", m); err != nil {
			fmt.Fprintf(os.Stderr, "error: git commit: %v: %s\n", err, out)
			return 1
		}
		fmt.Println("committed local changes")
	} else {
		fmt.Println("nothing to commit")
	}
	if _, err := runGit(root, "remote", "get-url", "origin"); err != nil {
		return 0
	}
	branch, _ := runGit(root, "rev-parse", "--abbrev-ref", "HEAD")
	if _, err := runGit(root, "ls-remote", "--exit-code", "--heads", "origin", branch); err == nil {
		if out, err := runGit(root, "pull", "--rebase", "origin", branch); err != nil {
			fmt.Fprintf(os.Stderr, "error: pull --rebase failed (resolve in %s, then re-run): %s\n", root, out)
			return 1
		}
	}
	if out, err := runGit(root, "push", "-u", "origin", branch); err != nil {
		fmt.Fprintf(os.Stderr, "error: push failed: %s\n", out)
		return 1
	}
	fmt.Println("pushed to origin/" + branch)
	return 0
}
