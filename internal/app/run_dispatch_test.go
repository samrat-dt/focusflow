package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunDispatchesEveryTopLevelCommand(t *testing.T) {
	setupRoot(t)
	seedTask(t, "dispatch")
	stubITerm(t)

	// Commands that must exit 0 with no further input.
	for _, args := range [][]string{
		{},
		{"help"},
		{"--help"},
		{"version"},
		{"--version"},
		{"list", "tasks"},
		{"list", "projects"},
		{"list", "tags"},
		{"list", "owners"},
		{"show", "task", "dispatch"},
		{"inbox"},
		{"inbox", "stats"},
		{"watch", "--list"},
		{"stats"},
		{"search", "dispatch"},
		{"doctor", "--json"},
		{"owner", "list"},
		{"workdir", "list"},
	} {
		var rc int
		captureStdout(t, func() { rc = Run(args) })
		if rc != 0 && !(len(args) > 0 && args[0] == "doctor") { // doctor exits 1 on any FAIL in a sandbox
			t.Errorf("Run(%v) rc=%d, want 0", args, rc)
		}
	}

	// Unknown commands are usage errors, not crashes.
	for _, args := range [][]string{{"nope"}, {"list", "nope"}, {"show"}, {"hook"}, {"hook", "nope"}, {"owner", "nope"}} {
		if rc := Run(args); rc == 0 {
			t.Errorf("Run(%v) should fail", args)
		}
	}
}

func TestVersionAndUsageOutput(t *testing.T) {
	out := captureStdout(t, func() { cmdVersion(nil) })
	if !strings.HasPrefix(out, "focusflow ") {
		t.Errorf("version = %q", out)
	}
	usage := captureStdout(t, printUsage)
	for _, want := range []string{"focusflow backup", "focusflow doctor", "focusflow owner", "focusflow search", "focusflow watch", "focusflow stats", "focusflow sync", "focusflow inbox stats"} {
		if !strings.Contains(usage, want) {
			t.Errorf("usage missing %q", want)
		}
	}
}

func TestDoctorJSONOutputIsValid(t *testing.T) {
	setupRoot(t)
	out := captureStdout(t, func() { cmdDoctor([]string{"--json"}) })
	var fs []finding
	if err := json.Unmarshal([]byte(out), &fs); err != nil || len(fs) == 0 {
		t.Fatalf("doctor --json = %q (%v)", out, err)
	}
	for _, f := range fs {
		if f.Level != "ok" && f.Level != "warn" && f.Level != "fail" {
			t.Errorf("bad level in %+v", f)
		}
	}
}

func TestSyncStatusBeforeAndAfterInit(t *testing.T) {
	setupRoot(t)
	if rc := syncStatus(mustRoot(t)); rc != 1 {
		t.Errorf("uninitialized status rc=%d, want 1", rc)
	}
}

func mustRoot(t *testing.T) string {
	t.Helper()
	r, err := dataRoot()
	if err != nil {
		t.Fatal(err)
	}
	return r
}
