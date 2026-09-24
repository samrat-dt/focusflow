package app

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"focusflow/internal/iterm"
)

// TestMain installs fail-safe defaults so a test that forgets to stub the
// terminal or the claude binary can never open a real tab or start a real
// session on the developer's machine. Tests that need a working spawn still
// replace these with stubITerm / stubExec, exactly as before.
func TestMain(m *testing.M) {
	iterm.Stub(func(args []string) (string, error) {
		return "", fmt.Errorf("test refused to spawn a real terminal (use stubITerm)")
	})
	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	}
	os.Exit(m.Run())
}
