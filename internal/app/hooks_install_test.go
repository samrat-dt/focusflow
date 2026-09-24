package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallManagesAllThreeHooksAndPreservesOthers(t *testing.T) {
	initTempRoot(t)
	path, _ := userSettingsPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"model":"x","hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"other"}]}],"Stop":[{"hooks":[{"type":"command","command":"someone-else"}]}]}}`), 0o640)

	changed, err := installSessionStartHook()
	if err != nil || !changed {
		t.Fatalf("install = %v, %v", changed, err)
	}
	if changed, _ := installSessionStartHook(); changed {
		t.Error("second install must be a no-op")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Errorf("settings mode changed to %v", fi.Mode().Perm())
	}

	_, settings, _, _ := readSettings()
	for _, spec := range managedHooks {
		if !hookInstalled(settings, spec) {
			t.Errorf("hook %s not installed", spec.Command)
		}
	}
	if settings["model"] != "x" {
		t.Error("unrelated top-level key lost")
	}
	hooks := settings["hooks"].(map[string]any)
	if len(hooks["PreToolUse"].([]any)) != 1 || len(hooks["Stop"].([]any)) != 2 {
		t.Errorf("foreign hooks disturbed: %v", hooks)
	}

	if changed, err := uninstallSessionStartHook(); err != nil || !changed {
		t.Fatalf("uninstall = %v, %v", changed, err)
	}
	raw, _ := os.ReadFile(path)
	var after map[string]any
	json.Unmarshal(raw, &after)
	_, _, _, _ = path, after, raw, 0
	h := after["hooks"].(map[string]any)
	if _, ok := h["SessionStart"]; ok {
		t.Error("SessionStart left behind")
	}
	if _, ok := h["UserPromptSubmit"]; ok {
		t.Error("UserPromptSubmit left behind")
	}
	if stop := h["Stop"].([]any); len(stop) != 1 || !hookEntryHasCommand(stop[0], "someone-else") {
		t.Errorf("foreign Stop hook not preserved: %v", stop)
	}
	if len(h["PreToolUse"].([]any)) != 1 {
		t.Error("PreToolUse lost")
	}
}

func TestInstallCreatesMissingSettingsFile(t *testing.T) {
	initTempRoot(t)
	if changed, err := installSessionStartHook(); err != nil || !changed {
		t.Fatalf("install on missing settings = %v, %v", changed, err)
	}
	_, s, existed, _ := readSettings()
	if !existed || !hookInstalled(s, managedHooks[2]) {
		t.Error("settings not created with hooks")
	}
}
