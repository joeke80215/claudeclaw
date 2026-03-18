// Package statusline writes the daemon state file for the statusline script.
package statusline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/claudeclaw/claudeclaw/internal/config"
)

// HeartbeatState holds the next heartbeat timestamp.
type HeartbeatState struct {
	NextAt int64 `json:"nextAt"`
}

// JobState holds a job name and its next scheduled run timestamp.
type JobState struct {
	Name   string `json:"name"`
	NextAt int64  `json:"nextAt"`
}

// WebState holds the web dashboard state for the statusline.
type WebState struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
}

// StateData represents the full daemon state written to state.json.
type StateData struct {
	Heartbeat *HeartbeatState `json:"heartbeat,omitempty"`
	Jobs      []JobState      `json:"jobs"`
	Security  string          `json:"security"`
	Telegram  bool            `json:"telegram"`
	Discord   bool            `json:"discord"`
	StartedAt int64           `json:"startedAt"`
	Web       *WebState       `json:"web,omitempty"`
}

// WriteState writes the state data to state.json in the claudeclaw directory.
func WriteState(state StateData) error {
	if state.Jobs == nil {
		state.Jobs = []JobState{}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("statusline: marshal: %w", err)
	}
	path := filepath.Join(config.BaseDir(), "state.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("statusline: write: %w", err)
	}
	return nil
}
