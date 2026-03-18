// Package web provides the ClaudeClaw web dashboard HTTP server and services.
package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/claudeclaw/claudeclaw/internal/config"
)

// SanitizeSettings returns a copy of settings with sensitive fields removed.
func SanitizeSettings(s config.Settings) map[string]interface{} {
	return map[string]interface{}{
		"timezone":              s.Timezone,
		"timezoneOffsetMinutes": s.TimezoneOffsetMinutes,
		"heartbeat":             s.Heartbeat,
		"security":              s.Security,
		"telegram": map[string]interface{}{
			"configured":       s.Telegram.Token != "",
			"allowedUserCount": len(s.Telegram.AllowedUserIds),
		},
		"discord": map[string]interface{}{
			"configured":       s.Discord.Token != "",
			"allowedUserCount": len(s.Discord.AllowedUserIds),
		},
		"web": s.Web,
	}
}

// BuildState builds the full daemon state response from a snapshot.
func BuildState(snapshot Snapshot) map[string]interface{} {
	now := time.Now().UnixMilli()

	var nextAt interface{}
	var nextInMs interface{}
	if snapshot.HeartbeatNextAt > 0 {
		nextAt = snapshot.HeartbeatNextAt
		diff := snapshot.HeartbeatNextAt - now
		if diff < 0 {
			diff = 0
		}
		nextInMs = diff
	}

	jobList := make([]map[string]interface{}, 0, len(snapshot.Jobs))
	for _, j := range snapshot.Jobs {
		jobList = append(jobList, map[string]interface{}{
			"name":     j.Name,
			"schedule": j.Schedule,
			"prompt":   j.Prompt,
		})
	}

	settings := snapshot.Settings
	if settings == nil {
		settings = &config.Settings{}
	}

	return map[string]interface{}{
		"daemon": map[string]interface{}{
			"running":   true,
			"pid":       snapshot.PID,
			"startedAt": snapshot.StartedAt,
			"uptimeMs":  now - snapshot.StartedAt,
		},
		"heartbeat": map[string]interface{}{
			"enabled":         settings.Heartbeat.Enabled,
			"intervalMinutes": settings.Heartbeat.Interval,
			"nextAt":          nextAt,
			"nextInMs":        nextInMs,
		},
		"jobs":     jobList,
		"security": settings.Security,
		"telegram": map[string]interface{}{
			"configured":       settings.Telegram.Token != "",
			"allowedUserCount": len(settings.Telegram.AllowedUserIds),
		},
		"discord": map[string]interface{}{
			"configured":       settings.Discord.Token != "",
			"allowedUserCount": len(settings.Discord.AllowedUserIds),
		},
		"web": settings.Web,
	}
}

// BuildTechnicalInfo reads raw JSON files and returns technical debug info.
func BuildTechnicalInfo() map[string]interface{} {
	baseDir := config.BaseDir()
	return map[string]interface{}{
		"files": map[string]interface{}{
			"settingsJson": readJSONFile(filepath.Join(baseDir, "settings.json")),
			"sessionJson":  readJSONFile(filepath.Join(baseDir, "session.json")),
			"stateJson":    readJSONFile(filepath.Join(baseDir, "state.json")),
		},
	}
}

func readJSONFile(path string) interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil
	}
	return v
}

// ReadLogs reads recent log files from the logs directory.
func ReadLogs(tail int) map[string]interface{} {
	if tail <= 0 {
		tail = 100
	}

	logsDir := config.LogsDir()
	daemonLog := readTail(filepath.Join(logsDir, "daemon.log"), tail)

	runs := readRecentRunLogs(logsDir, tail)
	return map[string]interface{}{
		"daemonLog": daemonLog,
		"runs":      runs,
	}
}

type logFileInfo struct {
	name  string
	path  string
	mtime time.Time
}

func readRecentRunLogs(logsDir string, tail int) []map[string]interface{} {
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return []map[string]interface{}{}
	}

	var candidates []logFileInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") || entry.Name() == "daemon.log" {
			continue
		}
		fullPath := filepath.Join(logsDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, logFileInfo{
			name:  entry.Name(),
			path:  fullPath,
			mtime: info.ModTime(),
		})
		if len(candidates) >= 200 {
			break
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mtime.After(candidates[j].mtime)
	})

	if len(candidates) > 5 {
		candidates = candidates[:5]
	}

	result := make([]map[string]interface{}, 0, len(candidates))
	for _, c := range candidates {
		result = append(result, map[string]interface{}{
			"file":  c.name,
			"lines": readTail(c.path, tail),
		})
	}
	return result
}

func readTail(path string, lines int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{}
	}
	all := strings.Split(string(data), "\n")
	// Filter empty trailing lines
	var filtered []string
	for _, line := range all {
		if line != "" {
			filtered = append(filtered, line)
		}
	}
	if len(filtered) > lines {
		filtered = filtered[len(filtered)-lines:]
	}
	if filtered == nil {
		filtered = []string{}
	}
	return filtered
}

var jobNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// CreateQuickJob creates a new quick job markdown file.
func CreateQuickJob(name, schedule, prompt string, recurring bool, notify string) error {
	name = strings.TrimSpace(name)
	schedule = strings.TrimSpace(schedule)
	prompt = strings.TrimSpace(prompt)

	if prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	if len(prompt) > 10000 {
		return fmt.Errorf("prompt too long")
	}

	// If name is empty, generate one from schedule
	if name == "" {
		stamp := time.Now().Format("20060102150405")
		name = fmt.Sprintf("quick-%s", stamp)
	}

	if !jobNameRe.MatchString(name) {
		return fmt.Errorf("invalid job name: must match [a-zA-Z0-9_-]+")
	}

	// If schedule looks like HH:MM, convert to cron
	if matched, _ := regexp.MatchString(`^\d{2}:\d{2}$`, schedule); matched {
		hour := schedule[:2]
		minute := schedule[3:5]
		schedule = fmt.Sprintf("%s %s * * *", minute, hour)
	}

	if schedule == "" {
		return fmt.Errorf("schedule is required")
	}

	recurringStr := "false"
	if recurring {
		recurringStr = "true"
	}

	notifyLine := ""
	if notify != "" {
		notifyLine = fmt.Sprintf("notify: %s\n", notify)
	}

	content := fmt.Sprintf("---\nschedule: \"%s\"\nrecurring: %s\n%s---\n%s\n", schedule, recurringStr, notifyLine, prompt)

	jobsDir := config.JobsDir()
	if err := os.MkdirAll(jobsDir, 0o755); err != nil {
		return fmt.Errorf("create jobs dir: %w", err)
	}

	path := filepath.Join(jobsDir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write job file: %w", err)
	}
	return nil
}

var deleteJobNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// DeleteJob deletes a job markdown file by name.
func DeleteJob(name string) error {
	name = strings.TrimSpace(name)
	if !deleteJobNameRe.MatchString(name) {
		return fmt.Errorf("invalid job name")
	}

	path := filepath.Join(config.JobsDir(), name+".md")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	return nil
}
