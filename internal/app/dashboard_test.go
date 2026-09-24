package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func dashFixture(t *testing.T, port int, token string) string {
	t.Helper()
	root := setupRoot(t)
	dir := filepath.Join(root, "dashboard")
	os.MkdirAll(dir, 0o700)
	if token != "" {
		os.WriteFile(filepath.Join(dir, "token"), []byte(token+"\n"), 0o600)
	}
	if port != 0 {
		os.WriteFile(filepath.Join(dir, "instance.json"), []byte(`{"tunables":{"port":`+itoa(port)+`}}`), 0o600)
	}
	return dir
}

func itoa(n int) string { return strconv.Itoa(n) }

func stubDash(t *testing.T, up bool) (*[]string, *[][]string) {
	t.Helper()
	var opened []string
	var lc [][]string
	oldOpen, oldDial, oldLC := openURL, dashboardDial, launchctl
	openURL = func(u string) error { opened = append(opened, u); return nil }
	dashboardDial = func(string) bool { return up }
	launchctl = func(args ...string) ([]byte, error) { lc = append(lc, args); return nil, os.ErrNotExist }
	t.Cleanup(func() { openURL, dashboardDial, launchctl = oldOpen, oldDial, oldLC })
	return &opened, &lc
}

func TestDashboardOpensWithTokenWhenRunning(t *testing.T) {
	dashFixture(t, 4188, "abc123")
	opened, lc := stubDash(t, true)
	if rc := cmdDashboard(nil); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if len(*opened) != 1 || (*opened)[0] != "http://127.0.0.1:4188/#token=abc123" {
		t.Errorf("opened %v", *opened)
	}
	if len(*lc) != 0 {
		t.Errorf("a running dashboard must not be restarted: %v", *lc)
	}
	out := captureStdout(t, func() { cmdDashboard([]string{"url"}) })
	if strings.TrimSpace(out) != "http://127.0.0.1:4188/#token=abc123" {
		t.Errorf("url = %q", out)
	}
	if cmdDashboard([]string{"status"}) != 0 {
		t.Error("status of a running dashboard is 0")
	}
}

func TestDashboardNotRunningAndNoLoginItemFailsClearly(t *testing.T) {
	dashFixture(t, 0, "tok")
	opened, _ := stubDash(t, false)
	if rc := cmdDashboard([]string{"open"}); rc != 1 || len(*opened) != 0 {
		t.Errorf("rc=%d opened=%v", rc, *opened)
	}
	if cmdDashboard([]string{"status"}) != 1 {
		t.Error("status of a stopped dashboard is 1")
	}
}

func TestDashboardRequiresSetupAndValidatesUsage(t *testing.T) {
	dashFixture(t, 0, "")
	stubDash(t, true)
	if cmdDashboard(nil) != 1 {
		t.Error("no token = not set up")
	}
	if cmdDashboard([]string{"bogus"}) != 2 {
		t.Error("unknown subcommand is a usage error")
	}
	if dashboardPort(t.TempDir()) != 4177 {
		t.Error("default port")
	}
}
