package app

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const dashboardLabel = "local.focusflow.dashboard"

// openURL opens a URL in the default browser. Indirected for tests.
var openURL = func(u string) error { return exec.Command("open", u).Run() }

// dashboardDial reports whether something answers on host:port. Indirected for tests.
var dashboardDial = func(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func dashboardHome() (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "dashboard"), nil
}

func dashboardPort(home string) int {
	b, err := os.ReadFile(filepath.Join(home, "instance.json"))
	if err != nil {
		return 4177
	}
	var cfg struct {
		Tunables struct {
			Port int `json:"port"`
		} `json:"tunables"`
	}
	if json.Unmarshal(b, &cfg) != nil || cfg.Tunables.Port == 0 {
		return 4177
	}
	return cfg.Tunables.Port
}

// startDashboard makes sure the dashboard's server is answering, starting its
// launchd job if needed. Returns whether it is up.
func startDashboard(home string, port int) bool {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	if dashboardDial(addr) {
		return true
	}
	uid := strconv.Itoa(os.Getuid())
	plist, _ := os.UserHomeDir()
	plist = filepath.Join(plist, "Library", "LaunchAgents", dashboardLabel+".plist")
	if _, err := launchctl("print", "gui/"+uid+"/"+dashboardLabel); err == nil {
		launchctl("kickstart", "-k", "gui/"+uid+"/"+dashboardLabel)
	} else if _, err := os.Stat(plist); err == nil {
		launchctl("bootstrap", "gui/"+uid, plist)
	} else {
		return false
	}
	for i := 0; i < 40; i++ {
		if dashboardDial(addr) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return dashboardDial(addr)
}

// cmdDashboard implements `focusflow dashboard [open|start|status|url]`.
func cmdDashboard(args []string) int {
	sub := "open"
	if len(args) > 0 {
		sub = args[0]
	}
	home, err := dashboardHome()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	port := dashboardPort(home)
	switch sub {
	case "status":
		if dashboardDial("127.0.0.1:" + strconv.Itoa(port)) {
			fmt.Printf("Focusflow is running on http://127.0.0.1:%d/\n", port)
			return 0
		}
		fmt.Println("Focusflow is not running (start it with: focusflow dashboard start)")
		return 1
	case "start", "open", "url":
		tokenBytes, terr := os.ReadFile(filepath.Join(home, "token"))
		token := strings.TrimSpace(string(tokenBytes))
		if terr != nil || token == "" {
			fmt.Fprintf(os.Stderr, "error: the Focusflow app is not set up yet (no access key in %s) — run `make dashboard` in the focusflow repository\n", home)
			return 1
		}
		if sub != "url" && !startDashboard(home, port) {
			fmt.Fprintln(os.Stderr, "error: Focusflow did not start (is its login item installed? `make dashboard`)")
			return 1
		}
		u := fmt.Sprintf("http://127.0.0.1:%d/#token=%s", port, token)
		switch sub {
		case "url":
			fmt.Println(u)
		case "open":
			if err := openURL(u); err != nil {
				fmt.Fprintf(os.Stderr, "error: could not open Focusflow in your browser: %v\n", err)
				return 1
			}
			fmt.Printf("Opened Focusflow on http://127.0.0.1:%d/\n", port)
		default:
			fmt.Printf("Focusflow is running on http://127.0.0.1:%d/\n", port)
		}
		return 0
	}
	fmt.Fprintln(os.Stderr, "error: usage: focusflow dashboard [open|start|status|url]")
	return 2
}
