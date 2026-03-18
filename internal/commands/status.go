package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/claudeclaw/claudeclaw/internal/config"
	"github.com/claudeclaw/claudeclaw/internal/pid"
)

// ANSI escape codes for terminal output.
const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
)

// Status shows the current daemon status. With --all, shows all daemons.
func Status(args []string) {
	for _, arg := range args {
		if arg == "--all" {
			showAll()
			return
		}
	}
	showStatus()
}

// formatCountdown formats a millisecond duration as a human-readable countdown.
func formatCountdown(ms int64) string {
	if ms <= 0 {
		return "now!"
	}
	s := ms / 1000
	h := s / 3600
	m := (s % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm", m)
	}
	return "<1m"
}

func showStatus() {
	pidPath := pid.GetPidPath()
	daemonRunning := false
	pidStr := ""

	data, err := os.ReadFile(pidPath)
	if err == nil {
		pidStr = strings.TrimSpace(string(data))
		pidNum, err := strconv.Atoi(pidStr)
		if err == nil && pidNum > 0 {
			if syscall.Kill(pidNum, 0) == nil {
				daemonRunning = true
			}
		}
	}

	if !daemonRunning {
		fmt.Printf("%s○ Daemon is not running%s\n", ansiRed, ansiReset)
		return
	}

	fmt.Printf("%s● Daemon is running%s (PID %s)\n", ansiGreen, ansiReset, pidStr)

	// Read settings
	settingsPath := config.SettingsPath()
	settingsData, err := os.ReadFile(settingsPath)
	if err == nil {
		var settings map[string]interface{}
		if json.Unmarshal(settingsData, &settings) == nil {
			if hb, ok := settings["heartbeat"].(map[string]interface{}); ok {
				enabled, _ := hb["enabled"].(bool)
				interval, _ := hb["interval"].(float64)
				if enabled {
					fmt.Printf("  Heartbeat: every %dm\n", int(interval))
				} else {
					fmt.Println("  Heartbeat: disabled")
				}

				tz := "system"
				if tzStr, ok := settings["timezone"].(string); ok && strings.TrimSpace(tzStr) != "" {
					tz = strings.TrimSpace(tzStr)
				}
				if enabled {
					fmt.Printf("  Heartbeat timezone: %s\n", tz)
					windows := 0
					if ew, ok := hb["excludeWindows"].([]interface{}); ok {
						windows = len(ew)
					}
					if windows > 0 {
						fmt.Printf("  Quiet windows: %d\n", windows)
					} else {
						fmt.Println("  Quiet windows: none")
					}
				}
			}
		}
	}

	// Read jobs
	jobsDir := config.JobsDir()
	entries, err := os.ReadDir(jobsDir)
	if err == nil {
		var mdFiles []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				mdFiles = append(mdFiles, e.Name())
			}
		}
		if len(mdFiles) > 0 {
			fmt.Printf("  Jobs: %d\n", len(mdFiles))
			scheduleRe := regexp.MustCompile(`schedule:\s*["']?([^"'\n]+)`)
			for _, f := range mdFiles {
				content, err := os.ReadFile(filepath.Join(jobsDir, f))
				if err != nil {
					continue
				}
				schedule := "unknown"
				if m := scheduleRe.FindStringSubmatch(string(content)); m != nil {
					schedule = strings.TrimSpace(m[1])
				}
				name := strings.TrimSuffix(f, ".md")
				fmt.Printf("    - %s [%s]\n", name, schedule)
			}
		}
	}

	// Read state
	cwd, _ := os.Getwd()
	stateFile := filepath.Join(cwd, ".claude", "claudeclaw", "state.json")
	stateData, err := os.ReadFile(stateFile)
	if err == nil {
		var state struct {
			Heartbeat *struct {
				NextAt int64 `json:"nextAt"`
			} `json:"heartbeat"`
			Jobs []struct {
				Name   string `json:"name"`
				NextAt int64  `json:"nextAt"`
			} `json:"jobs"`
		}
		if json.Unmarshal(stateData, &state) == nil {
			now := nowMs()
			fmt.Println()
			if state.Heartbeat != nil {
				fmt.Printf("  %s♥%s Next heartbeat: %s\n", ansiRed, ansiReset, formatCountdown(state.Heartbeat.NextAt-now))
			}
			for _, job := range state.Jobs {
				fmt.Printf("  → %s: %s\n", job.Name, formatCountdown(job.NextAt-now))
			}
		}
	}
}

func showAll() {
	daemons := findAllDaemons()
	if len(daemons) == 0 {
		fmt.Printf("%s○ No running daemons found%s\n", ansiRed, ansiReset)
		return
	}

	fmt.Printf("Found %d running daemon(s):\n\n", len(daemons))
	for _, d := range daemons {
		fmt.Printf("%s● Running%s PID %s — %s\n", ansiGreen, ansiReset, d.pid, d.path)
	}
}

type daemonInfo struct {
	path string
	pid  string
}

func findAllDaemons() []daemonInfo {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}

	var results []daemonInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidatePath := decodePath(entry.Name())
		pidFile := filepath.Join(candidatePath, ".claude", "claudeclaw", "daemon.pid")

		data, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		pidStr := strings.TrimSpace(string(data))
		pidNum, err := strconv.Atoi(pidStr)
		if err != nil || pidNum <= 0 {
			continue
		}

		if syscall.Kill(pidNum, 0) != nil {
			continue
		}

		results = append(results, daemonInfo{path: candidatePath, pid: pidStr})
	}
	return results
}
