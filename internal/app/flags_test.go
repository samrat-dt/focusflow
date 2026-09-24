package app_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// CLI flag-set contract tests. The public flags of these commands are
// what scripts, hooks and tend rely on, so they are pinned:
// removing or renaming one must be a deliberate change to the fixtures
// below.
//
// Some commands take a mandatory leading positional ref/name BEFORE flag
// parsing runs (`do <ref>`, `add task <name>`, `update task <ref>`, ...).
// Passing `--help` as that leading arg never reaches the flag parser (and
// for `add task` it would INSERT a row named "--help"). So those cases
// supply a harmless dummy positional and run in a sandboxed
// FOCUSFLOW_ROOT/HOME (see TestFlagSets_Pinned).
var flagNameRe = regexp.MustCompile(`(?m)^\s*-{1,2}([a-zA-Z][a-zA-Z0-9-]*)`)

func extractFlagNames(help string) []string {
	seen := map[string]bool{}
	for _, m := range flagNameRe.FindAllStringSubmatch(help, -1) {
		seen[m[1]] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// diffStrings returns elements of a not present in b.
func diffStrings(a, b []string) []string {
	inB := map[string]bool{}
	for _, s := range b {
		inB[s] = true
	}
	var out []string
	for _, s := range a {
		if !inB[s] {
			out = append(out, s)
		}
	}
	return out
}

// buildFocusflowBinary builds the current source into a temp binary once
// per test run, so the flag test always reflects the code under test,
// not a possibly-stale installed binary.
func buildFocusflowBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "focusflow")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "../.." // internal/app/flags_test.go -> repo root (where go.mod lives)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build focusflow: %v\n%s", err, stderr.String())
	}
	return binPath
}

func TestFlagSets_HelpGolden(t *testing.T) {
	binPath := buildFocusflowBinary(t)

	cases := []struct {
		fixture string
		args    []string
	}{
		{"transcript", []string{"transcript", "--help"}},
		{"list_tasks", []string{"list", "tasks", "--help"}},
		{"list_projects", []string{"list", "projects", "--help"}},
		{"list_playbooks", []string{"list", "playbooks", "--help"}},
		{"list_tags", []string{"list", "tags", "--help"}},
		{"list_runs", []string{"list", "runs", "--help"}},
	}

	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			fixturePath := filepath.Join("testdata", "help", tc.fixture+".txt")
			golden, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("missing golden fixture %s: %v", fixturePath, err)
			}
			wantFlags := extractFlagNames(string(golden))

			cmd := exec.Command(binPath, tc.args...)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			cmd.Run() // --help commonly exits nonzero via flag.ContinueOnError; ignore exit code, check output
			gotFlags := extractFlagNames(out.String())

			missing := diffStrings(wantFlags, gotFlags)
			extra := diffStrings(gotFlags, wantFlags)
			if len(missing) > 0 {
				t.Errorf("focusflow %v lost flags recorded in the golden help: %v", tc.args, missing)
			}
			if len(extra) > 0 {
				t.Errorf("focusflow %v gained flags not in the golden help (update testdata/help deliberately): %v", tc.args, extra)
			}
		})
	}
}

// TestFlagSets_Pinned pins the flag sets of the commands that take a
// leading positional, by re-parsing each command's own --help output.
func TestFlagSets_Pinned(t *testing.T) {
	binPath := buildFocusflowBinary(t)

	cases := []struct {
		name  string
		args  []string
		flags []string
	}{
		{
			// A dummy positional lets --help reach the flag parser; Go's flag
			// package intercepts it before any DB code runs.
			name: "add task",
			args: []string{"add", "task", "flagtest-dummy-name", "--help"},
			flags: []string{
				"slug", "project", "work-dir", "mkdir", "priority", "due", "assignee", "tag",
			},
		},
		{
			name: "do",
			args: []string{"do", "flagtest-dummy-ref", "--help"},
			flags: []string{
				"fresh", "dangerously-skip-permissions", "here", "force", "harness", "with", "with-file", "auto",
			},
		},
		{
			name: "update task",
			args: []string{"update", "task", "flagtest-dummy-ref", "--help"},
			flags: []string{
				"work-dir", "mkdir", "status", "priority", "assignee", "clear-assignee",
				"due-date", "clear-due", "waiting", "clear-waiting", "tag", "remove-tag", "clear-tags",
				"name", "project", "clear-project",
			},
		},
		{
			name:  "run playbook",
			args:  []string{"run", "playbook", "flagtest-dummy-slug", "--help"},
			flags: []string{"dangerously-skip-permissions", "here", "auto", "with", "with-file"},
		},
	}

	// Defense in depth: every exec gets its own throwaway FOCUSFLOW_ROOT and
	// HOME so it can only ever touch a temp dir.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sandboxDir := t.TempDir()
			cmd := exec.Command(binPath, tc.args...)
			cmd.Env = append(os.Environ(), "FOCUSFLOW_ROOT="+sandboxDir, "HOME="+sandboxDir)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			cmd.Run()
			got := extractFlagNames(out.String())

			want := append([]string{}, tc.flags...)
			sort.Strings(want)

			missing := diffStrings(want, got)
			extra := diffStrings(got, want)
			if len(missing) > 0 {
				t.Errorf("focusflow %v is missing pinned flags: %v\nfull output:\n%s", tc.args, missing, out.String())
			}
			if len(extra) > 0 {
				t.Errorf("focusflow %v has unexpected extra flags not in the pinned set: %v (update the pinned set deliberately)", tc.args, extra)
			}
		})
	}
}
