package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Stop reads the PID file, sends SIGTERM, and cleans up.
func Stop() {
	pidPath := pid_GetPidPath()
	data, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("No daemon is running (PID file not found).")
		os.Exit(0)
	}

	pidStr := strings.TrimSpace(string(data))
	pidNum, err := strconv.Atoi(pidStr)
	if err != nil || pidNum <= 0 {
		fmt.Println("Invalid PID file.")
		_ = os.Remove(pidPath)
		os.Exit(0)
	}

	if err := syscall.Kill(pidNum, syscall.SIGTERM); err != nil {
		fmt.Printf("Daemon process %s already dead.\n", pidStr)
	} else {
		fmt.Printf("Stopped daemon (PID %s).\n", pidStr)
	}

	_ = os.Remove(pidPath)

	// Teardown statusline
	teardownStatusline()

	// Remove state.json
	cwd, _ := os.Getwd()
	stateFile := filepath.Join(cwd, ".claude", "claudeclaw", "state.json")
	_ = os.Remove(stateFile)

	os.Exit(0)
}

// StopAll scans ~/.claude/projects/ for all daemon PIDs and stops each.
func StopAll() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("No projects found.")
		os.Exit(0)
	}

	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		fmt.Println("No projects found.")
		os.Exit(0)
	}

	found := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projectPath := decodePath(entry.Name())
		pidFile := filepath.Join(projectPath, ".claude", "claudeclaw", "daemon.pid")

		data, err := os.ReadFile(pidFile)
		if err != nil {
			continue
		}
		pidStr := strings.TrimSpace(string(data))
		pidNum, err := strconv.Atoi(pidStr)
		if err != nil || pidNum <= 0 {
			continue
		}

		// Check if process is alive
		if err := syscall.Kill(pidNum, 0); err != nil {
			continue
		}

		found++
		if err := syscall.Kill(pidNum, syscall.SIGTERM); err != nil {
			fmt.Printf("\x1b[31m✗ Failed to stop\x1b[0m PID %s — %s\n", pidStr, projectPath)
		} else {
			fmt.Printf("\x1b[33m■ Stopped\x1b[0m PID %s — %s\n", pidStr, projectPath)
			_ = os.Remove(pidFile)
		}
	}

	if found == 0 {
		fmt.Println("No running daemons found.")
	}

	os.Exit(0)
}

// decodePath converts an encoded directory name back to a filesystem path.
// The encoding replaces "/" with "-" and strips the leading "/".
func decodePath(encoded string) string {
	if len(encoded) > 0 {
		return "/" + strings.ReplaceAll(encoded[1:], "-", "/")
	}
	return "/"
}

// pid_GetPidPath returns the path to the daemon PID file.
// Duplicated here to avoid circular imports — the canonical implementation
// is in the pid package.
func pid_GetPidPath() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".claude", "claudeclaw", "daemon.pid")
}
