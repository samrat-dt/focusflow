// Package iterm spawns terminal tabs/windows via osascript. Despite the
// package name (kept for import stability) it supports iTerm2 and
// Terminal.app; see Backend.
package iterm

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Backend names the terminal application a session is spawned in.
type Backend string

const (
	ITerm2      Backend = "iTerm2"
	TerminalApp Backend = "Terminal.app"
)

// SelectBackend resolves which terminal to use from $FOCUSFLOW_TERM,
// defaulting to iTerm2. Unknown values are an error
// rather than a silent default, so a typo can't open the wrong app.
func SelectBackend() (Backend, error) {
	v := os.Getenv("FOCUSFLOW_TERM")
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "iterm", "iterm2", "iterm.app":
		return ITerm2, nil
	case "terminal", "terminal.app", "apple_terminal":
		return TerminalApp, nil
	}
	return "", fmt.Errorf("unsupported terminal %q (FOCUSFLOW_TERM must be iTerm2 or Terminal.app)", v)
}

// Runner is the function used to execute osascript.
// Tests override this to capture arguments without invoking osascript.
var Runner = func(args []string) error {
	cmd := exec.Command("osascript", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript failed: %v: %s", err, string(out))
	}
	return nil
}

// SpawnTab opens a new iTerm2 tab with the given title, cwd, and command.
// envVars are attached as an inline prefix to `command` only — so they
// are present in the spawned process's environment but do NOT persist in
// the tab's shell after the command exits.
//
// The typed line is prefixed with a single space so shells with
// `histignorespace` (zsh) or `HISTCONTROL=ignorespace`/`ignoreboth`
// (bash) skip writing it to the shared history file. Shells without
// that opt-in will still record the line — see README for the one-line
// shell config that turns it on.
func SpawnTab(title, cwd, command string, envVars map[string]string) error {
	envPrefix := ""
	if len(envVars) > 0 {
		keys := make([]string, 0, len(envVars))
		for k := range envVars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(envVars))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%s", k, ShellQuote(envVars[k])))
		}
		envPrefix = strings.Join(parts, " ") + " "
	}
	fullCommand := fmt.Sprintf(" cd %s && %s%s", ShellQuote(cwd), envPrefix, command)
	safeCommand := escapeAppleScriptString(fullCommand)
	safeTitle := escapeAppleScriptString(title)

	backend, err := SelectBackend()
	if err != nil {
		return err
	}
	return Runner([]string{"-e", spawnScript(backend, safeTitle, safeCommand)})
}

// spawnScript renders the AppleScript for a backend. iTerm2: reuse the
// front window when one exists, otherwise create one — `current window`
// is missing when the app has no windows (e.g. a launchd-started
// control-center), which used to fail the spawn outright. Terminal.app:
// `do script` opens a new window running the command, then it is titled.
func spawnScript(b Backend, safeTitle, safeCommand string) string {
	if b == TerminalApp {
		return fmt.Sprintf(`tell application "Terminal"
  set newTab to do script "%s"
  set custom title of newTab to "%s"
  activate
end tell
`, safeCommand, safeTitle)
	}
	return fmt.Sprintf(`tell application "iTerm2"
  if (count of windows) = 0 then
    set newWindow to (create window with default profile)
    tell current session of newWindow
      set name to "%s"
      write text "%s"
    end tell
  else
    tell current window
      set newTab to (create tab with default profile)
      tell current session of newTab
        set name to "%s"
        write text "%s"
      end tell
    end tell
  end if
end tell
`, safeTitle, safeCommand, safeTitle, safeCommand)
}

// ShellQuote wraps s in single quotes with proper escaping.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func escapeAppleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
