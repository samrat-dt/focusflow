package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func searchFixture(t *testing.T) string {
	t.Helper()
	root := setupRoot(t)
	cmdAdd([]string{"project", "Payments", "--slug", "pay", "--work-dir", t.TempDir()})
	cmdAdd([]string{"task", "Fix invoice rounding", "--slug", "invoice", "--project", "pay"})
	cmdAdd([]string{"task", "Unrelated chore", "--slug", "chore"})
	os.WriteFile(filepath.Join(root, "tasks/invoice/brief.md"), []byte("# Invoice\nThe Stripe webhook drops events\n"), 0o600)
	os.WriteFile(filepath.Join(root, "tasks/invoice/updates/2026-01-01-n.md"), []byte("tried stripe retries\n"), 0o600)
	os.WriteFile(filepath.Join(root, "tasks/chore/brief.md"), []byte("nothing about it\nstripe mentioned here too\n"), 0o600)
	os.WriteFile(filepath.Join(root, "kb/org.md"), []byte("- 2026-01-01 — we use Stripe\n"), 0o600)
	os.MkdirAll(filepath.Join(root, "tasks/invoice/workspace"), 0o700)
	os.WriteFile(filepath.Join(root, "tasks/invoice/workspace/code.md"), []byte("stripe in user work file\n"), 0o600)
	return root
}

func runSearch(t *testing.T, args ...string) []searchHit {
	t.Helper()
	out := captureStdout(t, func() {
		if rc := cmdSearch(append(args, "--json")); rc != 0 {
			t.Fatalf("search %v rc=%d", args, rc)
		}
	})
	var hits []searchHit
	if err := json.Unmarshal([]byte(out), &hits); err != nil {
		t.Fatalf("json %q: %v", out, err)
	}
	return hits
}

func TestSearchFindsAcrossBriefsUpdatesKBCaseInsensitive(t *testing.T) {
	searchFixture(t)
	hits := runSearch(t, "STRIPE")
	kinds := map[string]int{}
	for _, h := range hits {
		kinds[h.Kind]++
		if strings.Contains(h.File, "workspace") {
			t.Errorf("user workspace files must not be searched: %+v", h)
		}
	}
	if kinds["brief"] != 2 || kinds["update"] != 1 || kinds["kb"] != 1 {
		t.Errorf("kinds = %v (hits %+v)", kinds, hits)
	}
}

func TestSearchScopesAndFilters(t *testing.T) {
	searchFixture(t)
	if h := runSearch(t, "stripe", "--project", "pay"); len(h) != 2 {
		t.Errorf("--project pay: %d hits, want 2 (brief+update): %+v", len(h), h)
	}
	if h := runSearch(t, "stripe", "--in", "kb"); len(h) != 1 || h[0].Scope != "kb" {
		t.Errorf("--in kb: %+v", h)
	}
	if h := runSearch(t, "rounding", "--in", "names"); len(h) != 1 || h[0].Kind != "name" || h[0].Scope != "task:invoice" {
		t.Errorf("--in names: %+v", h)
	}
	if h := runSearch(t, "st.ipe|web(hook)", "--regex", "--in", "briefs"); len(h) == 0 {
		t.Errorf("regex found nothing")
	}
	if h := runSearch(t, "stripe", "--limit", "1"); len(h) != 1 {
		t.Errorf("--limit 1: %d hits", len(h))
	}
	if h := runSearch(t, "STRIPE", "--case"); len(h) != 0 {
		t.Errorf("--case should not match different case: %+v", h)
	}
}

func TestSearchSkipsArchivedUnlessAsked(t *testing.T) {
	searchFixture(t)
	if rc := cmdArchive([]string{"chore"}); rc != 0 {
		t.Fatal("archive")
	}
	for _, h := range runSearch(t, "stripe") {
		if h.Scope == "task:chore" {
			t.Errorf("archived task searched: %+v", h)
		}
	}
	found := false
	for _, h := range runSearch(t, "stripe", "--include-archived") {
		found = found || h.Scope == "task:chore"
	}
	if !found {
		t.Error("--include-archived should include archived tasks")
	}
}

func TestSearchUsageErrors(t *testing.T) {
	searchFixture(t)
	for name, args := range map[string][]string{
		"no query":    {},
		"bad regexp":  {"(", "--regex"},
		"bad --in":    {"x", "--in", "nope"},
		"bad project": {"x", "--project", "ghost"},
	} {
		if rc := cmdSearch(args); rc == 0 {
			t.Errorf("%s: expected failure", name)
		}
	}
	out := captureStdout(t, func() { cmdSearch([]string{"zzzz-no-such-thing"}) })
	if !strings.Contains(out, "(no matches)") {
		t.Errorf("empty result output: %q", out)
	}
}
