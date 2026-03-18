// Package sessions manages the global Claude Code session state.
package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/claudeclaw/claudeclaw/internal/config"
)

// GlobalSession represents a persisted session.
type GlobalSession struct {
	SessionId  string `json:"sessionId"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt"`
}

// SessionInfo is the minimal session info returned by GetSession.
type SessionInfo struct {
	SessionId string
}

var (
	mu      sync.RWMutex
	current *GlobalSession
)

func sessionFilePath() string {
	return filepath.Join(config.BaseDir(), "session.json")
}

func loadSession() (*GlobalSession, error) {
	if current != nil {
		return current, nil
	}
	data, err := os.ReadFile(sessionFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sessions: read: %w", err)
	}
	var s GlobalSession
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("sessions: parse: %w", err)
	}
	current = &s
	return current, nil
}

func saveSession(session *GlobalSession) error {
	current = session
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("sessions: marshal: %w", err)
	}
	if err := os.WriteFile(sessionFilePath(), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("sessions: write: %w", err)
	}
	return nil
}

// GetSession returns the existing session (updating lastUsedAt) or nil if none exists.
func GetSession() (*SessionInfo, error) {
	mu.Lock()
	defer mu.Unlock()

	existing, err := loadSession()
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}
	existing.LastUsedAt = time.Now().UTC().Format(time.RFC3339)
	if err := saveSession(existing); err != nil {
		return nil, err
	}
	return &SessionInfo{SessionId: existing.SessionId}, nil
}

// CreateSession saves a new session with the given session ID.
func CreateSession(sessionId string) error {
	mu.Lock()
	defer mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	return saveSession(&GlobalSession{
		SessionId:  sessionId,
		CreatedAt:  now,
		LastUsedAt: now,
	})
}

// PeekSession returns session metadata without mutating lastUsedAt.
func PeekSession() (*GlobalSession, error) {
	mu.RLock()
	defer mu.RUnlock()

	if current != nil {
		cp := *current
		return &cp, nil
	}
	data, err := os.ReadFile(sessionFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sessions: read: %w", err)
	}
	var s GlobalSession
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("sessions: parse: %w", err)
	}
	return &s, nil
}

// ResetSession deletes the session file and clears the in-memory cache.
func ResetSession() error {
	mu.Lock()
	defer mu.Unlock()

	current = nil
	err := os.Remove(sessionFilePath())
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sessions: remove: %w", err)
	}
	return nil
}

var backupRe = regexp.MustCompile(`^session_(\d+)\.backup$`)

// BackupSession renames the current session file to session_N.backup.
// Returns the backup filename, or empty string if no session existed.
func BackupSession() (string, error) {
	mu.Lock()
	defer mu.Unlock()

	existing, err := loadSession()
	if err != nil {
		return "", err
	}
	if existing == nil {
		return "", nil
	}

	baseDir := config.BaseDir()
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		entries = nil
	}

	maxIndex := 0
	for _, e := range entries {
		m := backupRe.FindStringSubmatch(e.Name())
		if m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > maxIndex {
				maxIndex = n
			}
		}
	}

	nextIndex := maxIndex + 1
	backupName := fmt.Sprintf("session_%d.backup", nextIndex)
	backupPath := filepath.Join(baseDir, backupName)

	if err := os.Rename(sessionFilePath(), backupPath); err != nil {
		return "", fmt.Errorf("sessions: rename to backup: %w", err)
	}
	current = nil

	return backupName, nil
}
