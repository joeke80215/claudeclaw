// Package jobs handles loading and managing cron job markdown files.
package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/claudeclaw/claudeclaw/internal/config"
)

// NotifyMode represents the notification setting for a job.
type NotifyMode int

const (
	NotifyTrue  NotifyMode = iota // always notify
	NotifyFalse                   // never notify
	NotifyError                   // notify only on error
)

// Job represents a parsed cron job from a .md file.
type Job struct {
	Name      string
	Schedule  string
	Prompt    string
	Recurring bool
	Notify    NotifyMode
}

var frontmatterRe = regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*\n(.*)$`)

func parseFrontmatterValue(raw string) string {
	trimmed := strings.TrimSpace(raw)
	// Strip surrounding quotes
	if len(trimmed) >= 2 {
		if (trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') ||
			(trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'') {
			trimmed = trimmed[1 : len(trimmed)-1]
		}
	}
	return trimmed
}

func parseJobFile(name, content string) *Job {
	m := frontmatterRe.FindStringSubmatch(content)
	if m == nil {
		fmt.Fprintf(os.Stderr, "Invalid job file format: %s\n", name)
		return nil
	}

	frontmatter := m[1]
	prompt := strings.TrimSpace(m[2])
	lines := strings.Split(frontmatter, "\n")

	var schedule string
	var recurringRaw string
	var notifyRaw string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "schedule:") {
			schedule = parseFrontmatterValue(strings.TrimPrefix(line, "schedule:"))
		} else if strings.HasPrefix(line, "recurring:") {
			recurringRaw = strings.ToLower(parseFrontmatterValue(strings.TrimPrefix(line, "recurring:")))
		} else if strings.HasPrefix(line, "daily:") {
			// Legacy alias
			if recurringRaw == "" {
				recurringRaw = strings.ToLower(parseFrontmatterValue(strings.TrimPrefix(line, "daily:")))
			}
		} else if strings.HasPrefix(line, "notify:") {
			notifyRaw = strings.ToLower(parseFrontmatterValue(strings.TrimPrefix(line, "notify:")))
		}
	}

	if schedule == "" {
		return nil
	}

	recurring := recurringRaw == "true" || recurringRaw == "yes" || recurringRaw == "1"

	var notify NotifyMode
	switch notifyRaw {
	case "false", "no":
		notify = NotifyFalse
	case "error":
		notify = NotifyError
	default:
		notify = NotifyTrue
	}

	return &Job{
		Name:      name,
		Schedule:  schedule,
		Prompt:    prompt,
		Recurring: recurring,
		Notify:    notify,
	}
}

// LoadJobs reads all .md files from the jobs directory and parses them into Jobs.
func LoadJobs() ([]Job, error) {
	jobsDir := config.JobsDir()
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Job{}, nil
		}
		return nil, fmt.Errorf("jobs: readdir: %w", err)
	}

	var jobs []Job
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(jobsDir, entry.Name()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "jobs: failed to read %s: %v\n", entry.Name(), err)
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".md")
		job := parseJobFile(name, string(content))
		if job != nil {
			jobs = append(jobs, *job)
		}
	}
	if jobs == nil {
		jobs = []Job{}
	}
	return jobs, nil
}

// ClearJobSchedule removes the schedule: line from a job's YAML frontmatter.
func ClearJobSchedule(jobName string) error {
	path := filepath.Join(config.JobsDir(), jobName+".md")
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("jobs: read %s: %w", path, err)
	}

	m := frontmatterRe.FindStringSubmatch(string(content))
	if m == nil {
		return nil
	}

	lines := strings.Split(m[1], "\n")
	var filtered []string
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "schedule:") {
			filtered = append(filtered, line)
		}
	}

	filteredFrontmatter := strings.TrimSpace(strings.Join(filtered, "\n"))
	body := strings.TrimSpace(m[2])
	next := fmt.Sprintf("---\n%s\n---\n%s\n", filteredFrontmatter, body)

	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return fmt.Errorf("jobs: write %s: %w", path, err)
	}
	return nil
}
