package app

import (
	"fmt"
	"focusflow/internal/flowdb"
	"os"
	"path/filepath"
)

// flowRoot returns the root directory for focusflow state. Honors $FOCUSFLOW_ROOT if
// set, otherwise falls back to ~/.focusflow. The returned path is not guaranteed
// to exist — callers that need it created should run `focusflow init` or create
// it themselves.
func flowRoot() (string, error) {
	if r := os.Getenv("FOCUSFLOW_ROOT"); r != "" {
		return r, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home dir: %w", err)
	}
	return filepath.Join(home, ".focusflow"), nil
}

// flowDBPath returns the absolute path to focusflow.db under the focusflow root.
// Returns a clear error if the data directory hasn't been initialized.
func flowDBPath() (string, error) {
	root, err := flowRoot()
	if err != nil {
		return "", err
	}
	dbPath := filepath.Join(root, "focusflow.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return "", fmt.Errorf("focusflow is not initialized — run `focusflow init` to set up %s", root)
	}
	return dbPath, nil
}

// cmdInit creates ~/.focusflow/ (or $FOCUSFLOW_ROOT), initializes focusflow.db, installs
// the skill. Idempotent — re-running is safe.
func cmdInit(args []string) int {
	fs := flagSet("init")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "error: init takes no positional arguments")
		return 2
	}

	root, err := flowRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Create the top-level tree. `projects/` and `tasks/` are parents for
	// per-project and per-task subdirectories created later by `focusflow add`.
	// `kb/` holds 5 knowledge-base files (user/org/products/processes/
	// business) that the skill appends to on the fly and that `focusflow show
	// task`/`show project` lists for execution sessions to read.
	for _, d := range []string{
		root,
		filepath.Join(root, "projects"),
		filepath.Join(root, "tasks"),
		filepath.Join(root, "kb"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error: mkdir %s: %v\n", d, err)
			return 1
		}
	}

	// Seed the KB files. Idempotent: only create if missing; never
	// overwrite existing content.
	for _, kb := range kbSeeds() {
		path := filepath.Join(root, "kb", kb.filename)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(kb.stub), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "error: write %s: %v\n", path, err)
				return 1
			}
		}
	}

	// Open (and implicitly initialize) focusflow.db.
	dbPath := filepath.Join(root, "focusflow.db")
	db, err := flowdb.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		return 1
	}
	db.Close()

	// Install the skill idempotently. Skip if already present; we never
	// overwrite on init (use `focusflow skill update` for that).
	skillPath, err := skillInstallPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error: create %s: %v\n", filepath.Dir(skillPath), err)
			return 1
		}
		if err := os.WriteFile(skillPath, embeddedSkill, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: write %s: %v\n", skillPath, err)
			return 1
		}
		if err := writeSkillVersion(Version); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not record skill version: %v\n", err)
		}
		fmt.Printf("installed focusflow skill to %s\n", skillPath)
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "error: stat %s: %v\n", skillPath, err)
		return 1
	}

	// Install the SessionStart hook idempotently.
	if added, err := installSessionStartHook(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not install SessionStart hook: %v\n", err)
	} else if added {
		settings, _ := userSettingsPath()
		fmt.Printf("installed SessionStart hook in %s\n", settings)
	}

	fmt.Printf("focusflow initialized at %s\n", root)
	fmt.Println(`Next: focusflow add project "My first project" --work-dir <path>`)
	return 0
}

// kbSeed describes one knowledge-base file to create on `focusflow init`.
type kbSeed struct {
	filename string
	stub     string
}

// kbSeeds returns the five canonical KB files. Kept as a function (not a
// package-level var) so tests can call it directly without sharing state.
// The skill is the authoritative source of how these files are appended
// to; this stub text is only visible to a human opening the file before
// any entries have been written.
func kbSeeds() []kbSeed {
	return []kbSeed{
		{"user.md", `# User

Durable facts about the person using focusflow — role, preferences, working
style, constraints, availability. The focusflow skill appends entries here
automatically when the user shares something in conversation.

<!-- Entries are appended as: "- YYYY-MM-DD — <short quote or paraphrase>" -->
`},
		{"org.md", `# Organization

The user's company, team, structure, stakeholders, reporting lines, and
people the user interacts with. Appended by the focusflow skill on the fly.

<!-- Entries are appended as: "- YYYY-MM-DD — <short quote or paraphrase>" -->
`},
		{"products.md", `# Products

What the org ships — product lines, modules, features, release cadence,
major capabilities. Appended by the focusflow skill on the fly.

<!-- Entries are appended as: "- YYYY-MM-DD — <short quote or paraphrase>" -->
`},
		{"processes.md", `# Processes

How the org works — tools, conventions, rituals, review rules, on-call,
deploy flows, meeting cadences. Appended by the focusflow skill on the fly.

<!-- Entries are appended as: "- YYYY-MM-DD — <short quote or paraphrase>" -->
`},
		{"business.md", `# Business

Customers, business model, revenue, deals, market positioning, contract
structure, pricing. Appended by the focusflow skill on the fly.

<!-- Entries are appended as: "- YYYY-MM-DD — <short quote or paraphrase>" -->
`},
	}
}

// kbFiles returns the absolute paths of all existing KB files under the
// given focusflow root, in the canonical order (user, org, products, processes,
// business). Paths for files that don't exist on disk are omitted — users
// can delete any kb file they don't care about without breaking show.
func kbFiles(root string) []string {
	var out []string
	for _, kb := range kbSeeds() {
		p := filepath.Join(root, "kb", kb.filename)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
