package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const backupSchedulerLabel = "local.focusflow.backup"

// backupPlist renders the launchd job that runs `focusflow backup` daily
// at 02:30 (launchd also runs a missed job when the Mac wakes).
func backupPlist(bin, root string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>%s</string><string>backup</string></array>
  <key>StartCalendarInterval</key>
  <dict><key>Hour</key><integer>2</integer><key>Minute</key><integer>30</integer></dict>
  <key>EnvironmentVariables</key>
  <dict><key>FOCUSFLOW_ROOT</key><string>%s</string></dict>
  <key>StandardOutPath</key><string>%s/backup.log</string>
  <key>StandardErrorPath</key><string>%s/backup.err.log</string>
</dict>
</plist>
`, backupSchedulerLabel, bin, root, root, root)
}

func backupPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", backupSchedulerLabel+".plist"), nil
}

// cmdBackupSchedule implements `focusflow backup schedule install|uninstall|status`.
func cmdBackupSchedule(args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	plist, err := backupPlistPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	uid := strconv.Itoa(os.Getuid())
	switch sub {
	case "install":
		bin, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		bin, _ = filepath.EvalSymlinks(bin)
		root, err := dataRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if err := os.WriteFile(plist, []byte(backupPlist(bin, root)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		launchctl("bootout", "gui/"+uid+"/"+backupSchedulerLabel)
		if out, err := launchctl("bootstrap", "gui/"+uid, plist); err != nil {
			fmt.Fprintf(os.Stderr, "error: launchctl bootstrap: %v: %s\n", err, strings.TrimSpace(string(out)))
			return 1
		}
		fmt.Printf("installed %s — daily backup at 02:30, newest %d kept in %s/backups\n", plist, defaultKeep, root)
		return 0
	case "uninstall":
		launchctl("bootout", "gui/"+uid+"/"+backupSchedulerLabel)
		if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Println("automatic backups removed")
		return 0
	case "status":
		if _, err := os.Stat(plist); err != nil {
			fmt.Println("automatic backups: not installed")
			return 0
		}
		if _, err := launchctl("print", "gui/"+uid+"/"+backupSchedulerLabel); err != nil {
			fmt.Println("automatic backups: installed but not loaded")
			return 0
		}
		fmt.Println("automatic backups: installed and loaded (daily 02:30)")
		return 0
	}
	fmt.Fprintln(os.Stderr, "error: usage: focusflow backup schedule install|uninstall|status")
	return 2
}
