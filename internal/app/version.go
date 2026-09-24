package app

import (
	"fmt"
	"runtime/debug"
)

// cmdVersion prints the build version plus VCS info embedded by the Go
// toolchain, so a stale installed binary is easy to spot (`focusflow
// version` in the shell vs. the one launchd or the hooks resolve).
func cmdVersion(args []string) int {
	commit, dirty := "", ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				commit = s.Value
				if len(commit) > 7 {
					commit = commit[:7]
				}
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "+dirty"
				}
			}
		}
	}
	if commit != "" {
		fmt.Printf("focusflow %s (%s%s)\n", Version, commit, dirty)
	} else {
		fmt.Printf("focusflow %s\n", Version)
	}
	return 0
}
