// Package pid manages the daemon PID file for process lifecycle tracking.
package pid

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/claudeclaw/claudeclaw/internal/config"
)

// GetPidPath returns the path to the daemon PID file.
func GetPidPath() string {
	return filepath.Join(config.BaseDir(), "daemon.pid")
}

// CheckExistingDaemon checks if a daemon is already running.
// Returns the PID if alive, 0 if not running. Cleans up stale PID files.
func CheckExistingDaemon() (int, error) {
	data, err := os.ReadFile(GetPidPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("pid: read: %w", err)
	}

	raw := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 0 {
		_ = CleanupPidFile()
		return 0, nil
	}

	// Signal 0 checks if the process exists without sending a signal
	err = syscall.Kill(pid, 0)
	if err != nil {
		// Process is dead, clean up stale PID file
		_ = CleanupPidFile()
		return 0, nil
	}

	return pid, nil
}

// WritePidFile writes the current process PID to the PID file.
func WritePidFile() error {
	content := strconv.Itoa(os.Getpid()) + "\n"
	if err := os.WriteFile(GetPidPath(), []byte(content), 0o644); err != nil {
		return fmt.Errorf("pid: write: %w", err)
	}
	return nil
}

// CleanupPidFile removes the PID file.
func CleanupPidFile() error {
	err := os.Remove(GetPidPath())
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("pid: remove: %w", err)
	}
	return nil
}
