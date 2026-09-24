package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"focusflow/internal/store"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// sessionRollup is what stats extracts from one session jsonl: summed
// token usage and the first/last record timestamps. It is cached by file
// (mod time + size) so repeated `focusflow stats` runs only re-read
// sessions that changed.
type sessionRollup struct {
	Input         int64  `json:"input"`
	Output        int64  `json:"output"`
	CacheCreation int64  `json:"cache_creation"`
	CacheRead     int64  `json:"cache_read"`
	First         string `json:"first"`
	Last          string `json:"last"`
}

type statsCacheEntry struct {
	ModNs  int64         `json:"mod_ns"`
	Size   int64         `json:"size"`
	Rollup sessionRollup `json:"rollup"`
}

type statsCache struct {
	Entries map[string]statsCacheEntry `json:"entries"`
}

func statsCachePath() (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "stats-cache.json"), nil
}

func loadStatsCache() statsCache {
	c := statsCache{Entries: map[string]statsCacheEntry{}}
	p, err := statsCachePath()
	if err != nil {
		return c
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(b, &c)
	if c.Entries == nil {
		c.Entries = map[string]statsCacheEntry{}
	}
	return c
}

func (c statsCache) save() {
	p, err := statsCachePath()
	if err != nil {
		return
	}
	if b, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(p, b, privFile)
	}
}

// rollupSession sums assistant-message usage in a session jsonl. Claude
// writes the same message (same id) once per content block, so usage is
// deduplicated by message id.
func rollupSession(path string) (sessionRollup, error) {
	f, err := os.Open(path)
	if err != nil {
		return sessionRollup{}, err
	}
	defer f.Close()

	type usage struct {
		In, Out, CC, CR int64
	}
	byMsg := map[string]usage{}
	var r sessionRollup
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var rec struct {
			Timestamp string `json:"timestamp"`
			Message   struct {
				ID    string `json:"id"`
				Usage struct {
					In  int64 `json:"input_tokens"`
					Out int64 `json:"output_tokens"`
					CC  int64 `json:"cache_creation_input_tokens"`
					CR  int64 `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		if rec.Timestamp != "" {
			if r.First == "" || rec.Timestamp < r.First {
				r.First = rec.Timestamp
			}
			if rec.Timestamp > r.Last {
				r.Last = rec.Timestamp
			}
		}
		u := rec.Message.Usage
		if rec.Message.ID != "" && (u.In+u.Out+u.CC+u.CR) > 0 {
			byMsg[rec.Message.ID] = usage{u.In, u.Out, u.CC, u.CR}
		}
	}
	if err := sc.Err(); err != nil {
		return r, err
	}
	for _, u := range byMsg {
		r.Input += u.In
		r.Output += u.Out
		r.CacheCreation += u.CC
		r.CacheRead += u.CR
	}
	return r, nil
}

type statsProjectRow struct {
	Slug                  string
	Tasks, Done, InFlight int
}

type statsReport struct {
	Since        string
	Project      string
	Backlog      int
	Next         int
	InProgress   int
	Paused       int
	Review       int
	Done         int
	DoneInWindow int
	Sessions     int
	Usage        sessionRollup
	Projects     []statsProjectRow
	Bus          *store.InboxStats
}

func cmdStats(args []string) int {
	fs := flagSet("stats")
	since := fs.String("since", "all", "window: all|<N>d|YYYY-MM-DD")
	project := fs.String("project", "", "limit to one project slug")
	card := fs.Bool("card", false, "also write a shareable HTML stats card")
	asJSON := fs.Bool("json", false, "machine-readable output (used by the dashboard)")
	out := fs.String("out", "", "card output path (default <root>/stats-card.html)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	var lower time.Time
	if *since != "all" {
		t, err := parseSince(*since, time.Now())
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 2
		}
		lower = t
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

	q := `SELECT ` + store.TaskCols + ` FROM tasks WHERE archived_at IS NULL AND kind = 'regular'`
	var qargs []any
	if *project != "" {
		q += ` AND project_slug = ?`
		qargs = append(qargs, *project)
	}
	rows, err := db.Query(q, qargs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	var tasks []*store.Task
	for rows.Next() {
		t, err := store.ScanTask(rows)
		if err != nil {
			rows.Close()
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		tasks = append(tasks, t)
	}
	rows.Close()

	rep := statsReport{Since: *since, Project: *project}
	byProject := map[string]*statsProjectRow{}
	cache := loadStatsCache()
	for _, t := range tasks {
		switch t.Status {
		case "backlog":
			rep.Backlog++
		case "next":
			rep.Next++
		case "paused":
			rep.Paused++
		case "review":
			rep.Review++
		case "in-progress":
			rep.InProgress++
		case "done":
			rep.Done++
			if inWindow(t.StatusChangedAt.String, lower) {
				rep.DoneInWindow++
			}
		}
		key := t.ProjectSlug.String
		if key == "" {
			key = "(floating)"
		}
		row := byProject[key]
		if row == nil {
			row = &statsProjectRow{Slug: key}
			byProject[key] = row
		}
		row.Tasks++
		if t.Status == "done" {
			row.Done++
		} else if t.Status == "in-progress" {
			row.InFlight++
		}

		if !t.SessionID.Valid || !inWindow(t.UpdatedAt, lower) {
			continue
		}
		p, err := sessionJSONLPath(t)
		if err != nil {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		ent, ok := cache.Entries[p]
		if !ok || ent.ModNs != st.ModTime().UnixNano() || ent.Size != st.Size() {
			r, err := rollupSession(p)
			if err != nil {
				continue
			}
			ent = statsCacheEntry{ModNs: st.ModTime().UnixNano(), Size: st.Size(), Rollup: r}
			cache.Entries[p] = ent
		}
		rep.Sessions++
		rep.Usage.Input += ent.Rollup.Input
		rep.Usage.Output += ent.Rollup.Output
		rep.Usage.CacheCreation += ent.Rollup.CacheCreation
		rep.Usage.CacheRead += ent.Rollup.CacheRead
	}
	cache.save()
	for _, r := range byProject {
		rep.Projects = append(rep.Projects, *r)
	}
	sort.Slice(rep.Projects, func(i, j int) bool { return rep.Projects[i].Slug < rep.Projects[j].Slug })
	if bs, err := store.InboxStatsFor(db, "user"); err == nil {
		rep.Bus = bs
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep.toJSON())
		return 0
	}
	fmt.Print(renderStatsText(rep))
	if *card {
		path := *out
		if path == "" {
			root, err := dataRoot()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			path = filepath.Join(root, "stats-card.html")
		}
		if err := os.WriteFile(path, []byte(renderStatsCard(rep)), privFile); err != nil {
			fmt.Fprintf(os.Stderr, "error: write card: %v\n", err)
			return 1
		}
		fmt.Printf("card written: %s\n", path)
	}
	return 0
}

func inWindow(ts string, lower time.Time) bool {
	if lower.IsZero() {
		return true
	}
	t, err := time.Parse(time.RFC3339, ts)
	return err == nil && !t.Before(lower)
}

func renderStatsText(r statsReport) string {
	var b strings.Builder
	title := "focusflow stats"
	if r.Project != "" {
		title += " — project " + r.Project
	}
	fmt.Fprintf(&b, "%s (since %s)\n\n", title, r.Since)
	fmt.Fprintf(&b, "tasks        : %d backlog, %d next, %d in-progress, %d paused, %d review, %d done (%d done in window)\n",
		r.Backlog, r.Next, r.InProgress, r.Paused, r.Review, r.Done, r.DoneInWindow)
	fmt.Fprintf(&b, "sessions     : %d\n", r.Sessions)
	fmt.Fprintf(&b, "tokens       : %d in, %d out, %d cache-write, %d cache-read\n", r.Usage.Input, r.Usage.Output, r.Usage.CacheCreation, r.Usage.CacheRead)
	if len(r.Projects) > 0 {
		b.WriteString("\nprojects:\n")
		for _, p := range r.Projects {
			fmt.Fprintf(&b, "  %-28s %3d tasks, %3d done, %3d in flight\n", p.Slug, p.Tasks, p.Done, p.InFlight)
		}
	}
	if r.Bus != nil {
		fmt.Fprintf(&b, "\nbus          : %d answered, %d pending, %d broadcasts, median wait %s\n", r.Bus.Answered, r.Bus.Pending, r.Bus.Broadcasts, formatWait(r.Bus.Median))
	}
	return b.String()
}

// renderStatsCard renders a small self-contained HTML card. It is
// dependency-free HTML — open it or screenshot it.)
func renderStatsCard(r statsReport) string {
	cell := func(label string, v any) string {
		return fmt.Sprintf(`<div class="c"><b>%v</b><span>%s</span></div>`, v, html.EscapeString(label))
	}
	title := "focusflow stats"
	if r.Project != "" {
		title += " · " + r.Project
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><html><head><meta charset="utf-8"><title>focusflow stats</title>
<style>body{font:15px system-ui;background:#111;color:#eee;margin:0;display:grid;place-items:center;min-height:100vh}
.card{background:#1b1b1f;border-radius:16px;padding:28px 32px;min-width:420px}h1{font-size:18px;margin:0 0 4px}p{color:#999;margin:0 0 20px}
.g{display:grid;grid-template-columns:repeat(3,1fr);gap:16px}.c b{display:block;font-size:26px}.c span{color:#999;font-size:12px}</style></head>
<body><div class="card"><h1>%s</h1><p>since %s</p><div class="g">`, html.EscapeString(title), html.EscapeString(r.Since))
	b.WriteString(cell("backlog", r.Backlog) + cell("in progress", r.InProgress) + cell("review", r.Review))
	b.WriteString(cell("done", r.Done))
	b.WriteString(cell("done in window", r.DoneInWindow) + cell("sessions", r.Sessions) + cell("output tokens", r.Usage.Output))
	b.WriteString(`</div></div></body></html>`)
	return b.String()
}

// statsJSON is the stable machine-readable shape of `stats --json`.
type statsJSON struct {
	Since        string         `json:"since"`
	Project      string         `json:"project,omitempty"`
	Tasks        map[string]int `json:"tasks"`
	Done         int            `json:"done"`
	DoneInWindow int            `json:"done_in_window"`
	Sessions     int            `json:"sessions"`
	Tokens       struct {
		Input         int64 `json:"input"`
		Output        int64 `json:"output"`
		CacheCreation int64 `json:"cache_creation"`
		CacheRead     int64 `json:"cache_read"`
	} `json:"tokens"`
	Projects []statsProjectJSON `json:"projects"`
	Bus      *struct {
		Answered          int     `json:"answered"`
		Pending           int     `json:"pending"`
		Broadcasts        int     `json:"broadcasts"`
		MedianWaitSeconds float64 `json:"median_wait_seconds"`
	} `json:"bus,omitempty"`
}

type statsProjectJSON struct {
	Slug     string `json:"slug"`
	Tasks    int    `json:"tasks"`
	Done     int    `json:"done"`
	InFlight int    `json:"in_flight"`
}

func (r statsReport) toJSON() statsJSON {
	var j statsJSON
	j.Since, j.Project = r.Since, r.Project
	j.Tasks = map[string]int{"backlog": r.Backlog, "next": r.Next, "in-progress": r.InProgress, "paused": r.Paused, "review": r.Review, "done": r.Done}
	j.Done, j.DoneInWindow, j.Sessions = r.Done, r.DoneInWindow, r.Sessions
	j.Tokens.Input, j.Tokens.Output, j.Tokens.CacheCreation, j.Tokens.CacheRead = r.Usage.Input, r.Usage.Output, r.Usage.CacheCreation, r.Usage.CacheRead
	j.Projects = []statsProjectJSON{}
	for _, p := range r.Projects {
		j.Projects = append(j.Projects, statsProjectJSON{p.Slug, p.Tasks, p.Done, p.InFlight})
	}
	if r.Bus != nil {
		j.Bus = &struct {
			Answered          int     `json:"answered"`
			Pending           int     `json:"pending"`
			Broadcasts        int     `json:"broadcasts"`
			MedianWaitSeconds float64 `json:"median_wait_seconds"`
		}{r.Bus.Answered, r.Bus.Pending, r.Bus.Broadcasts, r.Bus.Median.Seconds()}
	}
	return j
}
