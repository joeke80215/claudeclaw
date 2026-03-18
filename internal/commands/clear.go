package commands

import (
	"fmt"
	"os"

	"github.com/claudeclaw/claudeclaw/internal/pid"
	"github.com/claudeclaw/claudeclaw/internal/sessions"
)

// Clear backs up the current session and stops the daemon if running.
func Clear() {
	backup, err := sessions.BackupSession()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Session backup error: %v\n", err)
		os.Exit(1)
	}

	if backup != "" {
		fmt.Printf("Session backed up → %s\n", backup)
	} else {
		fmt.Println("No active session to back up.")
	}

	existingPid, err := pid.CheckExistingDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking daemon: %v\n", err)
		os.Exit(1)
	}

	if existingPid != 0 {
		fmt.Println("Stopping daemon so next start creates a fresh session...")
		Stop() // Stop calls os.Exit(0)
	} else {
		fmt.Println("No daemon running. Next start will create a new session.")
		os.Exit(0)
	}
}
