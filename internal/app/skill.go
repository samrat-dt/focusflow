package app

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed skill/SKILL.md
var embeddedSkill []byte

// hookCommand is the exact string written into settings.json under
// hooks.SessionStart so install/uninstall can idempotently find it.
// Keep it stable — changing this string would orphan existing
// installations.
const hookCommand = "focusflow hook session-start"

// hookMatcher is the SessionStart matcher string — fires on both
// fresh startup and `claude --resume`.
const hookMatcher = "startup|resume"

// skillInstallPath returns the absolute path where the skill should be
// installed on disk: ~/.claude/skills/focusflow/SKILL.md.
func skillInstallPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "skills", "focusflow", "SKILL.md"), nil
}

// skillVersionPath returns the sidecar file that records which binary
// version installed the current SKILL.md — used by the auto-upgrade
// check to decide whether to refresh the skill.
func skillVersionPath() (string, error) {
	skill, err := skillInstallPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(skill), "VERSION"), nil
}

// readSkillVersion returns the recorded version string, or "" if the
// sidecar file is missing or unreadable.
func readSkillVersion() string {
	p, err := skillVersionPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// writeSkillVersion records v as the version of the binary that
// installed the current SKILL.md. Errors are non-fatal — failing to
// write the sidecar should never block a successful skill install.
func writeSkillVersion(v string) error {
	p, err := skillVersionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(v+"\n"), 0o644)
}

// userSettingsPath returns ~/.claude/settings.json.
func userSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// maybeAutoUpgradeSkill checks the recorded skill version against the
// running binary's version and, if they differ, refreshes the skill +
// SessionStart hook. Designed to run on every focusflow invocation so the
// user gets a self-healing upgrade focusflow after replacing the binary.
//
// The check is intentionally conservative — it does nothing when:
//   - The binary is a "dev" build (Version == "dev"). Local devs use
//     `make install` and shouldn't fight an auto-installer.
//   - The skill isn't installed at all (sentinel: SKILL.md missing).
//     Treat this as an explicit user opt-out; never re-install.
//   - The recorded version already matches Version. The common path.
//
// All errors are silent — auto-upgrade is best-effort plumbing, not a
// command. A user-visible failure here would be far more annoying than
// the eventual symptom of a stale skill.
func maybeAutoUpgradeSkill() {
	if Version == "" || Version == "dev" {
		return
	}
	skillPath, err := skillInstallPath()
	if err != nil {
		return
	}
	if _, err := os.Stat(skillPath); err != nil {
		// Not installed → user opted out; don't reinstall behind their back.
		return
	}
	if readSkillVersion() == Version {
		return
	}
	// Version mismatch — refresh skill bytes and the SessionStart hook.
	if err := os.WriteFile(skillPath, embeddedSkill, 0o644); err != nil {
		return
	}
	_ = writeSkillVersion(Version)
	_, _ = installSessionStartHook()
	fmt.Fprintf(os.Stderr, "focusflow: upgraded skill to %s\n", Version)
}

// cmdSkill dispatches `focusflow skill install|uninstall|update`.
func cmdSkill(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: skill requires a subcommand (install|uninstall|update)")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "install":
		return skillInstall(rest, false)
	case "update":
		return skillInstall(rest, true)
	case "uninstall":
		return skillUninstall(rest)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown skill subcommand %q\n", sub)
		return 2
	}
}

func skillInstall(args []string, forceDefault bool) int {
	fs := flagSet("skill install")
	force := fs.Bool("force", forceDefault, "overwrite an existing installation")
	skipHook := fs.Bool("skip-hook", false, "don't auto-install the SessionStart hook in ~/.claude/settings.json")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	dest, err := skillInstallPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if _, err := os.Stat(dest); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "error: %s already exists; use --force to overwrite\n", dest)
		return 1
	} else if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: stat %s: %v\n", dest, err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: create %s: %v\n", filepath.Dir(dest), err)
		return 1
	}
	if err := os.WriteFile(dest, embeddedSkill, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: write %s: %v\n", dest, err)
		return 1
	}
	if err := writeSkillVersion(Version); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not record skill version: %v\n", err)
	}
	fmt.Printf("installed focusflow skill to %s\n", dest)

	if *skipHook {
		fmt.Println("--skip-hook: leaving ~/.claude/settings.json alone")
		return 0
	}
	if added, err := installSessionStartHook(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not install SessionStart hook: %v\n", err)
		// Non-fatal: the skill is still usable without the hook; the
		// user can wire it manually. Return 0 so `focusflow init` doesn't
		// fail on a settings.json quirk.
		return 0
	} else if added {
		settings, _ := userSettingsPath()
		fmt.Printf("installed focusflow hooks in %s (fires on startup + resume)\n", settings)
	} else {
		fmt.Println("SessionStart hook already installed — leaving as is")
	}
	return 0
}

func skillUninstall(args []string) int {
	fs := flagSet("skill uninstall")
	keepHook := fs.Bool("keep-hook", false, "don't remove the SessionStart hook from ~/.claude/settings.json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dest, err := skillInstallPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	skillDir := filepath.Dir(dest)
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		fmt.Printf("focusflow skill not installed at %s — nothing to do\n", skillDir)
	} else {
		if err := os.RemoveAll(skillDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: remove %s: %v\n", skillDir, err)
			return 1
		}
		fmt.Printf("uninstalled focusflow skill from %s\n", skillDir)
	}

	if *keepHook {
		fmt.Println("--keep-hook: leaving SessionStart hook in settings.json")
		return 0
	}
	if removed, err := uninstallSessionStartHook(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not remove SessionStart hook: %v\n", err)
		return 0
	} else if removed {
		settings, _ := userSettingsPath()
		fmt.Printf("removed focusflow hooks from %s\n", settings)
	}
	return 0
}

// hookSpec is one Claude Code hook focusflow owns in settings.json.
type hookSpec struct {
	Event   string // SessionStart | UserPromptSubmit | Stop
	Matcher string // "" = no matcher key
	Command string // exact command string; the idempotency key
}

// managedHooks is the full set install/uninstall/doctor manage. The
// commands are matched EXACTLY — never change them (that would orphan
// existing installations). Before this list existed only SessionStart was
// installed; the per-turn anchor and the Stop-hook bus nudge had to be
// wired by hand and were left behind by `skill uninstall`.
var managedHooks = []hookSpec{
	{Event: "SessionStart", Matcher: hookMatcher, Command: hookCommand},
	{Event: "UserPromptSubmit", Command: "focusflow hook user-prompt-submit"},
	{Event: "Stop", Command: "focusflow hook stop"},
}

// readSettings loads ~/.claude/settings.json (missing file = empty).
func readSettings() (path string, settings map[string]any, existed bool, err error) {
	path, err = userSettingsPath()
	if err != nil {
		return "", nil, false, err
	}
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		if !os.IsNotExist(rerr) {
			return path, nil, false, fmt.Errorf("read %s: %w", path, rerr)
		}
		return path, map[string]any{}, false, nil
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return path, nil, true, fmt.Errorf("parse %s: %w", path, err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return path, settings, true, nil
}

// writeSettings writes settings atomically (temp file + rename) and
// preserves the existing file mode, so a crash can never leave a
// truncated settings.json that breaks every Claude session.
func writeSettings(path string, settings map[string]any) error {
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// hookEntryHasCommand reports whether a settings hook entry runs cmd.
func hookEntryHasCommand(entry any, cmd string) bool {
	m, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	inner, _ := m["hooks"].([]any)
	for _, h := range inner {
		if hm, ok := h.(map[string]any); ok {
			if c, _ := hm["command"].(string); strings.TrimSpace(c) == cmd {
				return true
			}
		}
	}
	return false
}

// hookInstalled reports whether spec is wired in settings.
func hookInstalled(settings map[string]any, spec hookSpec) bool {
	hooks, _ := settings["hooks"].(map[string]any)
	list, _ := hooks[spec.Event].([]any)
	for _, e := range list {
		if hookEntryHasCommand(e, spec.Command) {
			return true
		}
	}
	return false
}

// installSessionStartHook idempotently wires every managed focusflow hook
// (SessionStart, UserPromptSubmit, Stop) into ~/.claude/settings.json and
// reports whether the file changed. The name is historical. Existing
// keys, other events and other entries are preserved.
func installSessionStartHook() (bool, error) {
	path, settings, _, err := readSettings()
	if err != nil {
		return false, err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	changed := false
	for _, spec := range managedHooks {
		if hookInstalled(settings, spec) {
			continue
		}
		entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": spec.Command}}}
		if spec.Matcher != "" {
			entry["matcher"] = spec.Matcher
		}
		list, _ := hooks[spec.Event].([]any)
		hooks[spec.Event] = append(list, entry)
		changed = true
	}
	if !changed {
		return false, nil
	}
	settings["hooks"] = hooks
	if err := writeSettings(path, settings); err != nil {
		return false, err
	}
	return true, nil
}

// uninstallSessionStartHook removes every managed focusflow hook, dropping
// an entry only when it held nothing else, and reports whether the file
// changed.
func uninstallSessionStartHook() (bool, error) {
	path, settings, existed, err := readSettings()
	if err != nil {
		return false, err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if !existed || hooks == nil {
		return false, nil
	}
	changed := false
	for _, spec := range managedHooks {
		list, _ := hooks[spec.Event].([]any)
		if len(list) == 0 {
			continue
		}
		kept := make([]any, 0, len(list))
		for _, entry := range list {
			m, ok := entry.(map[string]any)
			if !ok || !hookEntryHasCommand(entry, spec.Command) {
				kept = append(kept, entry)
				continue
			}
			inner, _ := m["hooks"].([]any)
			filtered := make([]any, 0, len(inner))
			for _, h := range inner {
				if hm, ok := h.(map[string]any); ok {
					if c, _ := hm["command"].(string); strings.TrimSpace(c) == spec.Command {
						changed = true
						continue
					}
				}
				filtered = append(filtered, h)
			}
			if len(filtered) > 0 {
				m["hooks"] = filtered
				kept = append(kept, m)
			}
		}
		if len(kept) == 0 {
			delete(hooks, spec.Event)
		} else {
			hooks[spec.Event] = kept
		}
	}
	if !changed {
		return false, nil
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	if err := writeSettings(path, settings); err != nil {
		return false, err
	}
	return true, nil
}
