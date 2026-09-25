package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"focusflow/internal/store"
)

// newMemoryID matches the message id shape (8 hex); overridable in tests.
var newMemoryID = func() (string, error) { return newMessageID() }

// rememberHuman records what the human told a task (see store.RememberHumanInput).
// Best effort: memory must never make sending a message fail, so errors only warn.
func rememberHuman(db *sql.DB, taskSlug, text, source string) {
	id, err := newMemoryID()
	if err != nil {
		return
	}
	if _, err := store.RememberHumanInput(db, id, taskSlug, text, source); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not remember this: %v\n", err)
	}
}

func openMemoryDB() (*sql.DB, int) {
	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, 1
	}
	db, err := openConcurrentDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, 1
	}
	return db, 0
}

func printRememberUsage() {
	fmt.Println(`focusflow remember "<text>" [--task <slug> | --project <slug> | --global]
                   [--kind decision|note|rule] [--pin] [--said] [--json]

Records something every future session in that scope should know.
Scope defaults to the task of the current session. --said records it as
something the human told the task (an answer to its pending question, or
an instruction), which is what the dashboard uses. --kind rule records a
house rule for a project or everywhere: every session there must follow it.`)
}

// cmdRemember implements `focusflow remember`.
func cmdRemember(args []string) int {
	var text, taskSlug, projectSlug, kind string
	var global, pin, said, asJSON bool
	kind = "decision"
	next := func(i *int, name string) (string, bool) {
		if *i+1 >= len(args) {
			fmt.Fprintf(os.Stderr, "error: %s needs a value\n", name)
			return "", false
		}
		*i++
		return args[*i], true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		var ok = true
		switch {
		case a == "-h" || a == "--help":
			printRememberUsage()
			return 0
		case a == "--task":
			taskSlug, ok = next(&i, a)
		case a == "--project":
			projectSlug, ok = next(&i, a)
		case a == "--kind":
			kind, ok = next(&i, a)
		case a == "--global":
			global = true
		case a == "--pin":
			pin = true
		case a == "--said":
			said = true
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(os.Stderr, "error: unknown flag %s\n", a)
			return 2
		default:
			if text != "" {
				fmt.Fprintln(os.Stderr, "error: give the text as one quoted argument")
				return 2
			}
			text = a
		}
		if !ok {
			return 2
		}
	}
	if strings.TrimSpace(text) == "" {
		printRememberUsage()
		return 2
	}
	if kind != "decision" && kind != "note" && kind != "rule" {
		fmt.Fprintln(os.Stderr, "error: --kind must be decision, note or rule")
		return 2
	}
	if kind == "rule" && taskSlug != "" {
		fmt.Fprintln(os.Stderr, "error: a house rule applies to a whole project (--project) or everywhere (--global), not one task")
		return 2
	}
	scopes := 0
	for _, b := range []bool{taskSlug != "", projectSlug != "", global} {
		if b {
			scopes++
		}
	}
	if scopes > 1 {
		fmt.Fprintln(os.Stderr, "error: pick one of --task, --project or --global")
		return 2
	}

	db, rc := openMemoryDB()
	if db == nil {
		return rc
	}
	defer db.Close()

	source := "cli"
	if cur := resolveCurrentTaskSlug(db); cur != "" {
		source = "agent"
		if scopes == 0 {
			taskSlug = cur
		}
	}
	if kind == "rule" && scopes == 0 && taskSlug != "" {
		// Inside a task session, a rule belongs to the task's project (or everywhere when it has none).
		t, err := ResolveTask(db, taskSlug, false)
		if err == nil && t.ProjectSlug.Valid {
			projectSlug, taskSlug = t.ProjectSlug.String, ""
		} else {
			global, taskSlug = true, ""
		}
		scopes = 1
	}
	if scopes == 0 && taskSlug == "" {
		fmt.Fprintln(os.Stderr, "error: not inside a task session; say where it applies with --task, --project or --global")
		return 2
	}

	id, err := newMemoryID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	var m *store.Memory
	if said {
		if taskSlug == "" {
			fmt.Fprintln(os.Stderr, "error: --said needs --task")
			return 2
		}
		if _, err := ResolveTask(db, taskSlug, false); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		m, err = store.RememberHumanInput(db, id, taskSlug, text, "dashboard")
	} else {
		m = &store.Memory{ID: id, Kind: kind, Body: store.CleanHumanText(text), Source: source, Pinned: pin, CreatedAt: store.NowISO()}
		switch {
		case global:
			m.Scope = "global"
		case projectSlug != "":
			if _, err := ResolveProject(db, projectSlug, false); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			m.Scope, m.ProjectSlug = "project", sql.NullString{String: projectSlug, Valid: true}
		default:
			t, err := ResolveTask(db, taskSlug, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			m.Scope, m.TaskSlug, m.ProjectSlug = "task", sql.NullString{String: t.Slug, Valid: true}, t.ProjectSlug
		}
		err = store.InsertMemory(db, m)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if m == nil {
		fmt.Fprintln(os.Stderr, "error: nothing to remember")
		return 1
	}
	if asJSON {
		return printJSON(memoryJSON(m))
	}
	fmt.Printf("Remembered [%s] (%s, %s)\n", m.ID, m.Kind, describeScope(m))
	return 0
}

func describeScope(m *store.Memory) string {
	switch m.Scope {
	case "global":
		return "everywhere"
	case "project":
		return "project " + m.ProjectSlug.String
	default:
		return "task " + m.TaskSlug.String
	}
}

type memoryOut struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Scope     string `json:"scope"`
	Task      string `json:"task,omitempty"`
	Project   string `json:"project,omitempty"`
	Question  string `json:"question,omitempty"`
	Body      string `json:"body"`
	Source    string `json:"source"`
	Pinned    bool   `json:"pinned"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func memoryJSON(m *store.Memory) memoryOut {
	return memoryOut{ID: m.ID, Kind: m.Kind, Scope: m.Scope, Task: m.TaskSlug.String, Project: m.ProjectSlug.String,
		Question: m.Question.String, Body: m.Body, Source: m.Source, Pinned: m.Pinned, Status: m.Status, CreatedAt: m.CreatedAt}
}

func printJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// cmdMemories implements `focusflow memories [--task <s> | --project <s> | --global] [--all] [--json]`.
func cmdMemories(args []string) int {
	var f store.MemoryFilter
	var asJSON bool
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-h", "--help":
			fmt.Println(`focusflow memories [--task <slug> | --project <slug> | --global] [--all] [--json]`)
			return 0
		case "--task", "--project":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "error: %s needs a value\n", a)
				return 2
			}
			i++
			if a == "--task" {
				f.TaskSlug = args[i]
			} else {
				f.ProjectSlug = args[i]
			}
		case "--global":
			f.GlobalOnly = true
		case "--all":
			f.IncludeGone = true
		case "--json":
			asJSON = true
		default:
			fmt.Fprintf(os.Stderr, "error: unexpected argument %q\n", a)
			return 2
		}
	}
	db, rc := openMemoryDB()
	if db == nil {
		return rc
	}
	defer db.Close()
	list, err := store.ListMemories(db, f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if asJSON {
		out := make([]memoryOut, 0, len(list))
		for _, m := range list {
			out = append(out, memoryJSON(m))
		}
		return printJSON(out)
	}
	if len(list) == 0 {
		fmt.Println("Nothing remembered yet.")
		return 0
	}
	for _, m := range list {
		pin := ""
		if m.Pinned {
			pin = " (pinned)"
		}
		gone := ""
		if m.Status != "active" {
			gone = " [forgotten]"
		}
		fmt.Printf("[%s] %s · %s · %s%s%s\n", m.ID, shortDate(m.CreatedAt), m.Kind, describeScope(m), pin, gone)
		if m.Question.Valid {
			fmt.Printf("    Q: %s\n", m.Question.String)
		}
		fmt.Printf("    %s\n", m.Body)
	}
	return 0
}

// cmdForget implements `focusflow forget <id>`.
func cmdForget(args []string) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Println(`focusflow forget <memory-id>`)
		if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
			return 0
		}
		return 2
	}
	return memoryOp("forget", args[0])
}

// cmdMemory implements `focusflow memory pin|unpin|promote|restore|forget <id>`.
func cmdMemory(args []string) int {
	if len(args) != 2 || strings.HasPrefix(args[1], "-") {
		fmt.Println(`focusflow memory pin|unpin|promote|restore|forget <memory-id>`)
		if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
			return 0
		}
		return 2
	}
	return memoryOp(args[0], args[1])
}

func memoryOp(op, id string) int {
	db, rc := openMemoryDB()
	if db == nil {
		return rc
	}
	defer db.Close()
	m, err := store.UpdateMemory(db, id, op)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("%s [%s] (%s)\n", map[string]string{"forget": "Forgot", "restore": "Restored", "pin": "Pinned", "unpin": "Unpinned", "promote": "Now applies to"}[op], m.ID, describeScope(m))
	return 0
}

func shortDate(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.Local().Format("2006-01-02")
}

// memoryContext renders what a session for task should already know, or ""
// when nothing is remembered. Capped so it never crowds out the brief.
func memoryContext(db *sql.DB, task *store.Task) string {
	global, project, own, err := store.RecallMemories(db, task.Slug, task.ProjectSlug.String, 15)
	if err != nil {
		return ""
	}
	rules, _ := store.RulesFor(db, task.ProjectSlug.String)
	if len(global)+len(project)+len(own)+len(rules) == 0 {
		return ""
	}
	var b strings.Builder
	if len(rules) > 0 {
		b.WriteString("HOUSE RULES (the user's standards, learned from their reviews; always follow them, and check your work against them before saying it is done):\n")
		for _, r := range rules {
			b.WriteString("- " + r.Body + "\n")
		}
		b.WriteString("\n")
	}
	if len(global)+len(project)+len(own) == 0 {
		return b.String()
	}
	b.WriteString("WHAT YOU ALREADY KNOW (from focusflow memory: things the user already answered or decided; " +
		"treat them as settled, do not ask them again; if one seems wrong or outdated, ask the user whether to change it, " +
		"and when the user states a new durable decision, record it with `focusflow remember \"<decision>\"`):\n")
	line := func(m *store.Memory) {
		s := "- " + shortDate(m.CreatedAt) + " "
		if m.Question.Valid {
			s += "Asked: \"" + m.Question.String + "\" Answer: "
		}
		s += m.Body
		if m.Pinned {
			s += " (pinned)"
		}
		b.WriteString(s + "\n")
	}
	section := func(title string, list []*store.Memory) {
		if len(list) == 0 {
			return
		}
		b.WriteString(title + ":\n")
		for _, m := range list {
			line(m)
		}
	}
	section("Everywhere", global)
	section("This project", project)
	section("This task", own)
	out := b.String()
	if r := []rune(out); len(r) > 4000 {
		out = string(r[:3999]) + "…\n"
	}
	return out
}
