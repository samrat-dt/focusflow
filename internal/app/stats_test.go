package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollupSessionDedupesByMessageID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{
		`{"timestamp":"2026-01-01T00:00:00Z","message":{"id":"m1","usage":{"input_tokens":2,"output_tokens":10,"cache_creation_input_tokens":5,"cache_read_input_tokens":100}}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","message":{"id":"m1","usage":{"input_tokens":2,"output_tokens":10,"cache_creation_input_tokens":5,"cache_read_input_tokens":100}}}`,
		`{"timestamp":"2026-01-01T00:05:00Z","message":{"id":"m2","usage":{"input_tokens":1,"output_tokens":4}}}`,
		`not json`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := rollupSession(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Input != 3 || r.Output != 14 || r.CacheCreation != 5 || r.CacheRead != 100 ||
		r.First != "2026-01-01T00:00:00Z" || r.Last != "2026-01-01T00:05:00Z" {
		t.Errorf("rollup = %+v", r)
	}
}

func TestCmdStatsTextAndCard(t *testing.T) {
	root := setupRoot(t)
	seedTask(t, "alpha")
	seedTask(t, "beta")
	if rc := cmdDone([]string{"beta"}); rc != 0 {
		t.Fatalf("done rc=%d", rc)
	}
	out := captureStdout(t, func() {
		if rc := cmdStats(nil); rc != 0 {
			t.Fatalf("stats rc=%d", rc)
		}
	})
	if !strings.Contains(out, "1 backlog") || !strings.Contains(out, "1 done") {
		t.Errorf("unexpected stats output:\n%s", out)
	}

	card := filepath.Join(root, "card.html")
	captureStdout(t, func() {
		if rc := cmdStats([]string{"--since", "7d", "--card", "--out", card}); rc != 0 {
			t.Fatalf("stats --card rc=%d", rc)
		}
	})
	b, err := os.ReadFile(card)
	if err != nil || !strings.Contains(string(b), "focusflow stats") {
		t.Errorf("card not written: %v", err)
	}
	if rc := cmdStats([]string{"--since", "garbage"}); rc != 2 {
		t.Errorf("bad --since rc=%d, want 2", rc)
	}
}
