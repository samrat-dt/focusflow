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

// This parity check is scoped to commands whose flags can be safely and
// accurately captured from the LIVE flow binary via `flow <cmd> --help`.
//
// Several flow commands take a mandatory leading positional ref/name
// BEFORE flag parsing runs (e.g. `do <ref>`, `add task <name>`, `update
// task <ref>`, `edit <ref>`, `archive <ref>`) — the code reads args[0] as
// the ref, then parses flags from the remainder. Passing `--help` as that
// leading arg never reaches the flag parser at all: for read-only
// commands (do/edit/archive/...) it just fails to resolve "--help" as an
// existing ref (harmless but useless for capturing real help text); for
// `add task`/`add project`/`add playbook` it actually INSERTS a new
// row named "--help", because those commands create from the positional
// unconditionally. This is exactly what happened earlier in this task's
// research phase against real flow (a stray task named "--help" was
// created and had to be archived) — see the flow-mirror/focusflow-parity
// session ledger. This test deliberately never invokes `--help` on any
// such command against live flow again.
//
// Commands covered here (`transcript`, and every `list <x>` subcommand)
// parse flags directly with no positional pre-extraction, so `--help`
// reaches the real flag parser and produces real, safe-to-capture usage
// text. Golden fixtures for the ref-taking commands this plan touched
// (`add task`, `do`, `update task`, `run playbook`) are instead the
// verified flag lists established via this session's own implementation
// work (Tasks 2, 4, 5, 6), cross-checked against the flow skill's
// embedded command reference and `strings` inspection of the live
// binary — see TestParity_KnownFlagSets below.
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
// per test run, so the parity test always reflects the code under test,
// not a possibly-stale installed binary.
func buildFocusflowBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "focusflow")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "../.." // internal/app/parity_test.go -> repo root (where go.mod lives)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build focusflow: %v\n%s", err, stderr.String())
	}
	return binPath
}

func TestParity_HelpFlagsMatchFlow(t *testing.T) {
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
			fixturePath := filepath.Join("testdata", "flow-help", tc.fixture+".txt")
			golden, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Skipf("no golden fixture for %s (%v) — capture it (read-only, from a command with no leading positional) if flow gained this command", tc.fixture, err)
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
				t.Errorf("focusflow %v is missing flags present in live flow: %v", tc.args, missing)
			}
			if len(extra) > 0 {
				t.Logf("focusflow %v has extra flags not in live flow (may be intentional): %v", tc.args, extra)
			}
		})
	}
}

// TestParity_KnownFlagSets pins the flag sets for commands whose live
// --help text cannot be safely/accurately captured (see the package
// comment above), using the flag lists verified during this session's
// research and implementation (Tasks 2, 4, 5, 6). This is the repeatable
// check for those commands: it re-parses each command's OWN --help
// output (safe — this is focusflow's binary, not live flow) and compares
// against the pinned, verified set. Update the pinned set only alongside
// new verified research against live flow, never to make a test pass.
func TestParity_KnownFlagSets(t *testing.T) {
	binPath := buildFocusflowBinary(t)

	cases := []struct {
		name  string
		args  []string
		flags []string
	}{
		{
			// "add task <dummy-name> --help" — NOT "add task --help". These
			// commands read their leading positional (name/ref/slug) into a
			// variable before parsing flags, so a bare "--help" as the FIRST
			// arg is consumed as that positional and never reaches the flag
			// parser — for add task specifically this actually INSERTS a row
			// named "--help" (confirmed the hard way earlier this session,
			// twice: once against real flow, once against this test's own
			// first unsandboxed draft against real local focusflow data —
			// both stray rows were archived, never deleted, per this plan's
			// "prefer reversible" discipline). Supplying a harmless dummy
			// positional lets --help reach the parser normally: Go's flag
			// package intercepts -h/--help before any of add task's DB code
			// runs, so this exec never opens a database at all.
			name: "add task",
			args: []string{"add", "task", "parity-test-dummy-name", "--help"},
			flags: []string{
				"slug", "project", "work-dir", "mkdir", "priority", "due", "assignee", "tag",
			},
		},
		{
			name: "do",
			args: []string{"do", "parity-test-dummy-ref", "--help"},
			flags: []string{
				"fresh", "dangerously-skip-permissions", "here", "force", "harness", "with", "with-file", "auto",
			},
		},
		{
			name: "update task",
			args: []string{"update", "task", "parity-test-dummy-ref", "--help"},
			flags: []string{
				"work-dir", "mkdir", "status", "priority", "assignee", "clear-assignee",
				"due-date", "clear-due", "waiting", "clear-waiting", "tag", "remove-tag", "clear-tags",
			},
		},
		{
			name:  "run playbook",
			args:  []string{"run", "playbook", "parity-test-dummy-slug", "--help"},
			flags: []string{"dangerously-skip-permissions", "here", "auto", "with", "with-file"},
		},
	}

	// Defense in depth beyond the dummy-positional trick above: every exec
	// gets its own throwaway FOCUSFLOW_ROOT and HOME so that even a parsing
	// path this comment didn't anticipate can only ever touch a temp dir,
	// never this machine's real ~/.focusflow or ~/.flow.
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
				t.Errorf("focusflow %v is missing verified flags: %v\nfull output:\n%s", tc.args, missing, out.String())
			}
			if len(extra) > 0 {
				t.Errorf("focusflow %v has unexpected extra flags not in the verified set: %v (update the pinned set only after re-verifying against live flow)", tc.args, extra)
			}
		})
	}
}
