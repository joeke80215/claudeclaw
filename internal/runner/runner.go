// Package runner executes Claude Code prompts via a serial queue with timeout,
// rate-limit detection, and fallback model support.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/claudeclaw/claudeclaw/internal/config"
	"github.com/claudeclaw/claudeclaw/internal/sessions"
	"github.com/claudeclaw/claudeclaw/internal/timezone"
)

// RunResult holds the output of a Claude Code execution.
type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// GracePeriod is the time to wait between SIGTERM and SIGKILL.
const GracePeriod = 5 * time.Second

var rateLimitPattern = regexp.MustCompile(`(?i)you.ve hit your limit|out of extra usage`)

// queue serializes Claude Code executions using a channel-based semaphore
// to prevent concurrent --resume on the same session.
type queue struct {
	mu sync.Mutex
	ch chan struct{} // semaphore of size 1
}

var q = &queue{
	ch: make(chan struct{}, 1),
}

func init() {
	// Initialize the semaphore with one permit.
	q.ch <- struct{}{}
}

// enqueue runs fn serially. Only one fn can execute at a time.
// It respects context cancellation while waiting for the semaphore.
func enqueue(ctx context.Context, fn func(ctx context.Context) (*RunResult, error)) (*RunResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case permit := <-q.ch:
		defer func() { q.ch <- permit }()
		return fn(ctx)
	}
}

// Managed block markers for CLAUDE.md.
const (
	ClaudeclawBlockStart = "<!-- claudeclaw:managed:start -->"
	ClaudeclawBlockEnd   = "<!-- claudeclaw:managed:end -->"
)

var dirScopePromptTemplate = strings.Join([]string{
	"CRITICAL SECURITY CONSTRAINT: You are scoped to the project directory: %s",
	"You MUST NOT read, write, edit, or delete any file outside this directory.",
	"You MUST NOT run bash commands that modify anything outside this directory (no cd /, no /etc, no ~/, no ../.. escapes).",
	"If a request requires accessing files outside the project, refuse and explain why.",
}, "\n")

// extractRateLimitMessage checks stdout and stderr for rate limit messages.
// Returns the matched text, or empty string if no rate limit detected.
func extractRateLimitMessage(stdout, stderr string) string {
	for _, text := range []string{stdout, stderr} {
		trimmed := strings.TrimSpace(text)
		if trimmed != "" && rateLimitPattern.MatchString(trimmed) {
			return trimmed
		}
	}
	return ""
}

// buildChildEnv creates the environment for the child Claude process.
// It strips the CLAUDECODE variable to prevent nested detection and configures
// authentication and base URL based on the model.
func buildChildEnv(baseEnv []string, model, api string) []string {
	env := make([]string, 0, len(baseEnv)+3)
	for _, e := range baseEnv {
		if strings.HasPrefix(e, "CLAUDECODE=") {
			continue
		}
		env = append(env, e)
	}

	normalizedModel := strings.ToLower(strings.TrimSpace(model))

	if trimmedAPI := strings.TrimSpace(api); trimmedAPI != "" {
		env = append(env, "ANTHROPIC_AUTH_TOKEN="+trimmedAPI)
	}

	if normalizedModel == "glm" {
		env = append(env, "ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic")
		env = append(env, "API_TIMEOUT_MS=3000000")
	}

	return env
}

// buildSecurityArgs generates CLI flags based on the security configuration.
func buildSecurityArgs(security config.SecurityConfig) []string {
	args := []string{"--dangerously-skip-permissions"}

	switch security.Level {
	case config.SecurityLocked:
		args = append(args, "--tools", "Read,Grep,Glob")
	case config.SecurityStrict:
		args = append(args, "--disallowedTools", "Bash,WebSearch,WebFetch")
	case config.SecurityModerate:
		// All tools available, scoped to project dir via system prompt.
	case config.SecurityUnrestricted:
		// All tools, no directory restriction.
	}

	if len(security.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(security.AllowedTools, " "))
	}
	if len(security.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(security.DisallowedTools, " "))
	}

	return args
}

// runClaudeOnce executes the claude CLI once with the given arguments, model, and API key.
// It uses context for timeout/cancellation and sets up a process group for clean termination.
func runClaudeOnce(ctx context.Context, baseArgs []string, model, api string, baseEnv []string) (rawStdout, stderr string, exitCode int) {
	args := make([]string, len(baseArgs))
	copy(args, baseArgs)

	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	if strings.TrimSpace(model) != "" && normalizedModel != "glm" {
		args = append(args, "--model", strings.TrimSpace(model))
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = buildChildEnv(baseEnv, model, api)

	// Set process group so we can kill the entire group on timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Override the default context cancellation behavior: we handle
	// the kill ourselves with SIGTERM -> grace period -> SIGKILL.
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = GracePeriod

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	rawStdout = stdoutBuf.String()
	stderr = stderrBuf.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return rawStdout, stderr, exitCode
}

// LoadPrompts loads and concatenates all prompt files from the prompts/ directory.
func LoadPrompts() (string, error) {
	promptsDir := config.PromptsDir()
	selectedFiles := []string{
		filepath.Join(promptsDir, "IDENTITY.md"),
		filepath.Join(promptsDir, "USER.md"),
		filepath.Join(promptsDir, "SOUL.md"),
	}

	var parts []string
	for _, file := range selectedFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			log.Printf("Failed to read prompt file %s: %v", file, err)
			continue
		}
		if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}

	return strings.Join(parts, "\n\n"), nil
}

// LoadHeartbeatPromptTemplate loads the heartbeat prompt template.
// A project-level override at .claude/claudeclaw/prompts/HEARTBEAT.md takes precedence
// over the built-in template at prompts/heartbeat/HEARTBEAT.md.
func LoadHeartbeatPromptTemplate() (string, error) {
	projectOverride := filepath.Join(config.ProjectPromptsDir(), "HEARTBEAT.md")
	builtinPath := filepath.Join(config.PromptsDir(), "heartbeat", "HEARTBEAT.md")

	for _, file := range []string{projectOverride, builtinPath} {
		data, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			log.Printf("Failed to read heartbeat prompt file %s: %v", file, err)
			continue
		}
		if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
			return trimmed, nil
		}
	}
	return "", nil
}

// EnsureProjectClaudeMd creates the project CLAUDE.md with managed block markers
// if it does not already exist. If a legacy .claude/CLAUDE.md exists, its content
// is merged into the new file.
func EnsureProjectClaudeMd() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("runner: getwd: %w", err)
	}

	projectClaudeMd := filepath.Join(cwd, "CLAUDE.md")
	legacyClaudeMd := filepath.Join(cwd, ".claude", "CLAUDE.md")

	// Never rewrite an existing project CLAUDE.md.
	if _, err := os.Stat(projectClaudeMd); err == nil {
		return nil
	}

	promptContent, err := LoadPrompts()
	if err != nil {
		return fmt.Errorf("runner: load prompts: %w", err)
	}
	promptContent = strings.TrimSpace(promptContent)

	managedBlock := strings.Join([]string{
		ClaudeclawBlockStart,
		promptContent,
		ClaudeclawBlockEnd,
	}, "\n")

	var content string

	// Try to read legacy .claude/CLAUDE.md.
	if data, readErr := os.ReadFile(legacyClaudeMd); readErr == nil {
		content = strings.TrimSpace(string(data))
	}

	normalized := strings.TrimSpace(content)
	hasManagedBlock := strings.Contains(normalized, ClaudeclawBlockStart) &&
		strings.Contains(normalized, ClaudeclawBlockEnd)

	var merged string
	if hasManagedBlock {
		pattern := regexp.MustCompile(
			regexp.QuoteMeta(ClaudeclawBlockStart) +
				`[\s\S]*?` +
				regexp.QuoteMeta(ClaudeclawBlockEnd))
		merged = pattern.ReplaceAllString(normalized, managedBlock) + "\n"
	} else if normalized != "" {
		merged = normalized + "\n\n" + managedBlock + "\n"
	} else {
		merged = managedBlock + "\n"
	}

	if err := os.WriteFile(projectClaudeMd, []byte(merged), 0o644); err != nil {
		return fmt.Errorf("runner: write CLAUDE.md: %w", err)
	}
	return nil
}

// execClaude runs a single Claude Code prompt, handling session management,
// rate limiting, fallback models, and logging.
func execClaude(ctx context.Context, name, prompt string) (*RunResult, error) {
	logsDir := config.LogsDir()
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil, fmt.Errorf("runner: mkdir logs: %w", err)
	}

	existing, err := sessions.GetSession()
	if err != nil {
		return nil, fmt.Errorf("runner: get session: %w", err)
	}
	isNew := existing == nil

	ts := time.Now().UTC().Format("2006-01-02T15-04-05")
	logFile := filepath.Join(logsDir, fmt.Sprintf("%s-%s.log", name, ts))

	settings := config.GetSettings()
	primaryConfig := config.ModelConfig{Model: settings.Model, API: settings.API}
	fallbackConfig := config.ModelConfig{
		Model: settings.Fallback.Model,
		API:   settings.Fallback.API,
	}
	securityArgs := buildSecurityArgs(settings.Security)

	sessionDesc := "new session"
	if !isNew {
		sid := existing.SessionId
		if len(sid) > 8 {
			sid = sid[:8]
		}
		sessionDesc = fmt.Sprintf("resume %s", sid)
	}
	log.Printf("Running: %s (%s, security: %s)", name, sessionDesc, settings.Security.Level)

	// New session: use json output to capture Claude's session_id.
	// Resumed session: use text output with --resume.
	outputFormat := "text"
	if isNew {
		outputFormat = "json"
	}

	args := []string{"claude", "-p", prompt, "--output-format", outputFormat}
	args = append(args, securityArgs...)

	if !isNew {
		args = append(args, "--resume", existing.SessionId)
	}

	// Build the appended system prompt: prompt files + directory scoping.
	// This is passed on EVERY invocation because --append-system-prompt
	// does not persist across --resume.
	promptContent, _ := LoadPrompts()
	appendParts := []string{"You are running inside ClaudeClaw."}
	if promptContent != "" {
		appendParts = append(appendParts, promptContent)
	}

	// Load the project's CLAUDE.md if it exists.
	cwd, _ := os.Getwd()
	projectClaudeMd := filepath.Join(cwd, "CLAUDE.md")
	if data, readErr := os.ReadFile(projectClaudeMd); readErr == nil {
		if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
			appendParts = append(appendParts, trimmed)
		}
	}

	if settings.Security.Level != config.SecurityUnrestricted {
		appendParts = append(appendParts, fmt.Sprintf(dirScopePromptTemplate, cwd))
	}
	if len(appendParts) > 0 {
		args = append(args, "--append-system-prompt", strings.Join(appendParts, "\n\n"))
	}

	baseEnv := os.Environ()

	rawStdout, stderr, exitCode := runClaudeOnce(ctx, args, primaryConfig.Model, primaryConfig.API, baseEnv)

	// Check for rate limiting and retry with fallback if available.
	primaryRateLimit := extractRateLimitMessage(rawStdout, stderr)
	usedFallback := false

	if primaryRateLimit != "" &&
		config.HasModelConfig(fallbackConfig) &&
		!config.SameModelConfig(primaryConfig, fallbackConfig) {

		fbDesc := "fallback"
		if fallbackConfig.Model != "" {
			fbDesc = fmt.Sprintf("fallback (%s)", fallbackConfig.Model)
		}
		log.Printf("Claude limit reached; retrying with %s...", fbDesc)

		rawStdout, stderr, exitCode = runClaudeOnce(ctx, args, fallbackConfig.Model, fallbackConfig.API, baseEnv)
		usedFallback = true
	}

	stdout := rawStdout
	sessionId := "unknown"
	if existing != nil {
		sessionId = existing.SessionId
	}

	rateLimitMessage := extractRateLimitMessage(rawStdout, stderr)
	if rateLimitMessage != "" {
		stdout = rateLimitMessage
	}

	// For new sessions, parse the JSON to extract session_id and result text.
	if rateLimitMessage == "" && isNew && exitCode == 0 {
		var jsonResp struct {
			SessionID string `json:"session_id"`
			Result    string `json:"result"`
		}
		if jsonErr := json.Unmarshal([]byte(rawStdout), &jsonResp); jsonErr == nil {
			sessionId = jsonResp.SessionID
			stdout = jsonResp.Result
			if createErr := sessions.CreateSession(sessionId); createErr != nil {
				log.Printf("Failed to save session: %v", createErr)
			} else {
				log.Printf("Session created: %s", sessionId)
			}
		} else {
			log.Printf("Failed to parse session from Claude output: %v", jsonErr)
		}
	}

	result := &RunResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
	}

	// Write execution log file.
	modelDesc := "primary"
	if usedFallback {
		modelDesc = "fallback"
	}
	sessionType := "resumed"
	if isNew {
		sessionType = "new"
	}

	var logBuf strings.Builder
	fmt.Fprintf(&logBuf, "# %s\n", name)
	fmt.Fprintf(&logBuf, "Date: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&logBuf, "Session: %s (%s)\n", sessionId, sessionType)
	fmt.Fprintf(&logBuf, "Model config: %s\n", modelDesc)
	fmt.Fprintf(&logBuf, "Prompt: %s\n", prompt)
	fmt.Fprintf(&logBuf, "Exit code: %d\n", result.ExitCode)
	fmt.Fprintf(&logBuf, "\n## Output\n%s\n", stdout)
	if stderr != "" {
		fmt.Fprintf(&logBuf, "## Stderr\n%s\n", stderr)
	}

	if writeErr := os.WriteFile(logFile, []byte(logBuf.String()), 0o644); writeErr != nil {
		log.Printf("Failed to write log file %s: %v", logFile, writeErr)
	}
	log.Printf("Done: %s -> %s", name, logFile)

	return result, nil
}

// Run enqueues a named prompt for serial execution via Claude Code.
// The serial queue prevents concurrent --resume on the same session.
func Run(ctx context.Context, name, prompt string) (*RunResult, error) {
	return enqueue(ctx, func(ctx context.Context) (*RunResult, error) {
		return execClaude(ctx, name, prompt)
	})
}

// prefixUserMessageWithClock prepends the current timestamp with timezone offset
// to user messages for time-awareness.
func prefixUserMessageWithClock(prompt string) string {
	offsetMinutes := 0
	func() {
		defer func() { recover() }()
		settings := config.GetSettings()
		offsetMinutes = settings.TimezoneOffsetMinutes
	}()

	prefix := timezone.BuildClockPromptPrefix(time.Now(), offsetMinutes)
	return prefix + "\n" + prompt
}

// RunUserMessage prepends a clock prefix to the prompt and enqueues it for execution.
func RunUserMessage(ctx context.Context, name, prompt string) (*RunResult, error) {
	return Run(ctx, name, prefixUserMessageWithClock(prompt))
}

// Bootstrap creates an initial session by firing Claude with a wake-up prompt.
// It is a no-op if a session already exists.
func Bootstrap(ctx context.Context) error {
	existing, err := sessions.GetSession()
	if err != nil {
		return fmt.Errorf("runner: bootstrap get session: %w", err)
	}
	if existing != nil {
		return nil
	}

	log.Println("Bootstrapping new session...")
	_, err = execClaude(ctx, "bootstrap", "Wakeup, my friend!")
	if err != nil {
		return fmt.Errorf("runner: bootstrap: %w", err)
	}
	log.Println("Bootstrap complete — session is live.")
	return nil
}
