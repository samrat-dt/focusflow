package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"focusflow/internal/store"
)

type searchHit struct {
	Scope string `json:"scope"` // task:<slug> | project:<slug> | kb
	Kind  string `json:"kind"`  // name | brief | update | note | kb
	File  string `json:"file,omitempty"`
	Line  int    `json:"line,omitempty"`
	Text  string `json:"text"`
}

func classifyFile(rel string) string {
	base := filepath.Base(rel)
	switch {
	case base == "brief.md":
		return "brief"
	case strings.Contains(filepath.ToSlash(rel), "/updates/"):
		return "update"
	case strings.HasPrefix(filepath.ToSlash(rel), "kb/"):
		return "kb"
	}
	return "note"
}

// searchTree greps the markdown under root (briefs, updates, sidecar
// notes, KB) for re. allowedTasks/allowedProjects, when non-nil, restrict
// task and project directories to those slugs (so archived work is
// skipped unless asked for); kinds filters by hit kind.
func searchTree(root string, re *regexp.Regexp, kinds map[string]bool, allowedTasks, allowedProjects map[string]bool, limit int) ([]searchHit, error) {
	var hits []searchHit
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if d.IsDir() {
			switch {
			case rel == backupDirName:
				return filepath.SkipDir
			case len(parts) == 2 && parts[0] == "tasks" && allowedTasks != nil && !allowedTasks[parts[1]]:
				return filepath.SkipDir
			case len(parts) == 2 && parts[0] == "projects" && allowedProjects != nil && !allowedProjects[parts[1]]:
				return filepath.SkipDir
			case len(parts) >= 3 && parts[2] == "workspace":
				return filepath.SkipDir // user work files are not notes
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") || len(parts) < 2 {
			return nil
		}
		kind := classifyFile(rel)
		if !kinds[kind] {
			return nil
		}
		scope := "kb"
		if parts[0] == "tasks" || parts[0] == "projects" {
			scope = strings.TrimSuffix(parts[0], "s") + ":" + parts[1]
		} else if parts[0] != "kb" {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for n := 1; sc.Scan(); n++ {
			if line := sc.Text(); re.MatchString(line) {
				hits = append(hits, searchHit{Scope: scope, Kind: kind, File: filepath.ToSlash(rel), Line: n, Text: strings.TrimSpace(truncateRunes(line, 200))})
				if limit > 0 && len(hits) >= limit {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].File < hits[j].File })
	return hits, err
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func cmdSearch(args []string) int {
	fs := flagSet("search")
	in := fs.String("in", "all", "comma list of: names,briefs,updates,notes,kb (or all)")
	project := fs.String("project", "", "only tasks/notes of this project")
	useRegex := fs.Bool("regex", false, "treat the query as a regular expression")
	caseSens := fs.Bool("case", false, "case-sensitive match")
	limit := fs.Int("limit", 50, "maximum hits (0 = unlimited)")
	archived := fs.Bool("include-archived", false, "include archived tasks and projects")
	asJSON := fs.Bool("json", false, "machine-readable output")
	// Accept the query before or after flags.
	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		fmt.Fprintln(os.Stderr, "error: usage: focusflow search <query> [--in ...] [--project <slug>] [--regex] [--json]")
		return 2
	}
	query := positional[0]

	kinds := map[string]bool{}
	names := false
	for _, k := range strings.Split(*in, ",") {
		switch strings.TrimSpace(k) {
		case "all":
			names = true
			for _, kk := range []string{"brief", "update", "note", "kb"} {
				kinds[kk] = true
			}
		case "names":
			names = true
		case "briefs":
			kinds["brief"] = true
		case "updates":
			kinds["update"] = true
		case "notes":
			kinds["note"] = true
		case "kb":
			kinds["kb"] = true
		default:
			fmt.Fprintf(os.Stderr, "error: unknown --in value %q\n", k)
			return 2
		}
	}

	pattern := query
	if !*useRegex {
		pattern = regexp.QuoteMeta(query)
	}
	if !*caseSens {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: bad pattern: %v\n", err)
		return 2
	}

	root, err := dataRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	dbPath, err := dataDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		return 1
	}
	defer db.Close()

	// Which tasks/projects are in scope.
	allowedTasks, allowedProjects := map[string]bool{}, map[string]bool{}
	var hits []searchHit
	tq := `SELECT slug, name, COALESCE(project_slug,'') FROM tasks`
	pq := `SELECT slug, name FROM projects`
	var conds []string
	if !*archived {
		conds = append(conds, "archived_at IS NULL")
	}
	if *project != "" {
		if _, err := ResolveProject(db, *project, *archived); err != nil {
			fmt.Fprintf(os.Stderr, "error: project %q not found\n", *project)
			return 1
		}
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	rows, err := db.Query(tq + where)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	for rows.Next() {
		var slug, name, proj string
		if err := rows.Scan(&slug, &name, &proj); err != nil {
			rows.Close()
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if *project != "" && proj != *project {
			continue
		}
		allowedTasks[slug] = true
		if names && (re.MatchString(slug) || re.MatchString(name)) {
			hits = append(hits, searchHit{Scope: "task:" + slug, Kind: "name", Text: name})
		}
	}
	rows.Close()
	prows, err := db.Query(pq + where)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	for prows.Next() {
		var slug, name string
		if err := prows.Scan(&slug, &name); err != nil {
			prows.Close()
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if *project != "" && slug != *project {
			continue
		}
		allowedProjects[slug] = true
		if names && (re.MatchString(slug) || re.MatchString(name)) {
			hits = append(hits, searchHit{Scope: "project:" + slug, Kind: "name", Text: name})
		}
	}
	prows.Close()

	if *project != "" {
		kinds["kb"] = false // KB is global, not per-project
	}
	fileHits, err := searchTree(root, re, kinds, allowedTasks, allowedProjects, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	hits = append(hits, fileHits...)
	total := len(hits)
	if *limit > 0 && len(hits) > *limit {
		hits = hits[:*limit]
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if hits == nil {
			hits = []searchHit{}
		}
		enc.Encode(hits)
		return 0
	}
	if len(hits) == 0 {
		fmt.Println("(no matches)")
		return 0
	}
	for _, h := range hits {
		if h.File != "" {
			fmt.Printf("%-28s %s:%d: %s\n", h.Scope, h.File, h.Line, h.Text)
		} else {
			fmt.Printf("%-28s name: %s\n", h.Scope, h.Text)
		}
	}
	if total > len(hits) {
		fmt.Printf("… %d more (raise --limit)\n", total-len(hits))
	}
	return 0
}
