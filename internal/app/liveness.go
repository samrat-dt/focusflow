package app

import (
	"os/exec"
	"strings"
)

// psLines returns the command line of every running process, one per
// line. Indirected as a package var so tests can substitute canned
// output without touching the real process table — same mocking
// pattern as iterm.Runner.
var psLines = defaultPSLines

func defaultPSLines() ([]string, error) {
	out, err := exec.Command("ps", "-axo", "command=").Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(string(out), "\n"), nil
}

// isSessionLive reports whether some running process's command line
// shows it is actively serving sessionID — i.e. a `claude --session-id
// <id>` (fresh bootstrap) or `claude --resume <id>` (resumed) process.
// A ps failure is treated as "not live" rather than propagated, since
// liveness is a best-effort display hint, not a correctness-critical
// value.
func isSessionLive(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	lines, err := psLines()
	if err != nil {
		return false
	}
	needleSessionID := "--session-id " + sessionID
	needleResume := "--resume " + sessionID
	for _, line := range lines {
		if strings.Contains(line, needleSessionID) || strings.Contains(line, needleResume) {
			return true
		}
	}
	return false
}
