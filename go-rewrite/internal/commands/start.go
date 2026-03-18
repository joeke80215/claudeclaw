// Package commands implements the CLI command handlers for ClaudeClaw.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/claudeclaw/claudeclaw/internal/config"
	"github.com/claudeclaw/claudeclaw/internal/cron"
	"github.com/claudeclaw/claudeclaw/internal/discord"
	"github.com/claudeclaw/claudeclaw/internal/jobs"
	"github.com/claudeclaw/claudeclaw/internal/pid"
	"github.com/claudeclaw/claudeclaw/internal/runner"
	"github.com/claudeclaw/claudeclaw/internal/statusline"
	"github.com/claudeclaw/claudeclaw/internal/telegram"
	"github.com/claudeclaw/claudeclaw/internal/timezone"
	"github.com/claudeclaw/claudeclaw/internal/web"
)

// claudeDir returns the .claude directory in the current working directory.
func claudeDir() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".claude")
}

func statuslineFilePath() string {
	return filepath.Join(claudeDir(), "statusline.cjs")
}

func claudeSettingsFilePath() string {
	return filepath.Join(claudeDir(), "settings.json")
}

// STATUSLINE_SCRIPT is the Node.js script embedded for the statusline display.
const STATUSLINE_SCRIPT = `#!/usr/bin/env node
const { readFileSync } = require("fs");
const { join } = require("path");

const DIR = join(__dirname, "claudeclaw");
const STATE_FILE = join(DIR, "state.json");
const PID_FILE = join(DIR, "daemon.pid");

const R = "\x1b[0m";
const DIM = "\x1b[2m";
const RED = "\x1b[31m";
const GREEN = "\x1b[32m";

function fmt(ms) {
  if (ms <= 0) return GREEN + "now!" + R;
  var s = Math.floor(ms / 1000);
  var h = Math.floor(s / 3600);
  var m = Math.floor((s % 3600) / 60);
  if (h > 0) return h + "h " + m + "m";
  if (m > 0) return m + "m";
  return (s % 60) + "s";
}

function alive() {
  try {
    var pid = readFileSync(PID_FILE, "utf-8").trim();
    process.kill(Number(pid), 0);
    return true;
  } catch { return false; }
}

var B = DIM + "\u2502" + R;
var TL = DIM + "\u256d" + R;
var TR = DIM + "\u256e" + R;
var BL = DIM + "\u2570" + R;
var BR = DIM + "\u256f" + R;
var H = DIM + "\u2500" + R;
var HEADER = TL + H.repeat(6) + " \ud83e\udd9e ClaudeClaw \ud83e\udd9e " + H.repeat(6) + TR;
var FOOTER = BL + H.repeat(30) + BR;

if (!alive()) {
  process.stdout.write(
    HEADER + "\n" +
    B + "        " + RED + "\u25cb offline" + R + "              " + B + "\n" +
    FOOTER
  );
  process.exit(0);
}

try {
  var state = JSON.parse(readFileSync(STATE_FILE, "utf-8"));
  var now = Date.now();
  var info = [];

  if (state.heartbeat) {
    info.push("\ud83d\udc93 " + fmt(state.heartbeat.nextAt - now));
  }

  var jc = (state.jobs || []).length;
  info.push("\ud83d\udccb " + jc + " job" + (jc !== 1 ? "s" : ""));
  info.push(GREEN + "\u25cf live" + R);

  if (state.telegram) {
    info.push(GREEN + "\ud83d\udce1" + R);
  }

  if (state.discord) {
    info.push(GREEN + "\ud83c\udfae" + R);
  }

  var mid = " " + info.join(" " + B + " ") + " ";

  process.stdout.write(HEADER + "\n" + B + mid + B + "\n" + FOOTER);
} catch {
  process.stdout.write(
    HEADER + "\n" +
    B + DIM + "         waiting...         " + R + B + "\n" +
    FOOTER
  );
}
`

var allDays = []int{0, 1, 2, 3, 4, 5, 6}

var clockRe = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

func parseClockMinutes(value string) (int, bool) {
	m := clockRe.FindStringSubmatch(value)
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	return h*60 + min, true
}

func isHeartbeatExcludedAt(cfg config.HeartbeatConfig, tzOffset int, at time.Time) bool {
	if len(cfg.ExcludeWindows) == 0 {
		return false
	}
	day, minute := timezone.GetDayAndMinuteAtOffset(at, tzOffset)

	for _, w := range cfg.ExcludeWindows {
		start, ok1 := parseClockMinutes(w.Start)
		end, ok2 := parseClockMinutes(w.End)
		if !ok1 || !ok2 {
			continue
		}
		days := w.Days
		if len(days) == 0 {
			days = allDays
		}

		sameDay := start < end

		if sameDay {
			if containsInt(days, day) && minute >= start && minute < end {
				return true
			}
			continue
		}

		if start == end {
			if containsInt(days, day) {
				return true
			}
			continue
		}

		// Overnight window
		if minute >= start && containsInt(days, day) {
			return true
		}
		previousDay := (day + 6) % 7
		if minute < end && containsInt(days, previousDay) {
			return true
		}
	}
	return false
}

func isHeartbeatExcludedNow(cfg config.HeartbeatConfig, tzOffset int) bool {
	return isHeartbeatExcludedAt(cfg, tzOffset, time.Now())
}

func nextAllowedHeartbeatAt(cfg config.HeartbeatConfig, tzOffset int, intervalMs int64, fromMs int64) int64 {
	interval := intervalMs
	if interval < 60000 {
		interval = 60000
	}
	candidate := fromMs + interval
	guard := 0
	for isHeartbeatExcludedAt(cfg, tzOffset, time.UnixMilli(candidate)) && guard < 20000 {
		candidate += interval
		guard++
	}
	return candidate
}

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

func setupStatusline() error {
	dir := claudeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(statuslineFilePath(), []byte(STATUSLINE_SCRIPT), 0o644); err != nil {
		return err
	}

	settingsPath := claudeSettingsFilePath()
	var settings map[string]interface{}
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	if settings == nil {
		settings = make(map[string]interface{})
	}
	settings["statusLine"] = map[string]interface{}{
		"type":    "command",
		"command": "node .claude/statusline.cjs",
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, append(out, '\n'), 0o644)
}

func teardownStatusline() {
	settingsPath := claudeSettingsFilePath()
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		var settings map[string]interface{}
		if json.Unmarshal(data, &settings) == nil {
			delete(settings, "statusLine")
			out, err := json.MarshalIndent(settings, "", "  ")
			if err == nil {
				_ = os.WriteFile(settingsPath, append(out, '\n'), 0o644)
			}
		}
	}
	_ = os.Remove(statuslineFilePath())
}

func ts() string {
	return time.Now().Format("15:04:05")
}

func nowMs() int64 {
	return time.Now().UnixMilli()
}

// Start implements the "start" command — either one-shot or daemon mode.
func Start(args []string) {
	var (
		hasPromptFlag      bool
		hasTriggerFlag     bool
		telegramFlag       bool
		discordFlag        bool
		debugFlag          bool
		webFlag            bool
		replaceExisting    bool
		webPortFlag        int
		webPortFlagSet     bool
		payloadParts       []string
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--prompt":
			hasPromptFlag = true
		case "--trigger":
			hasTriggerFlag = true
		case "--telegram":
			telegramFlag = true
		case "--discord":
			discordFlag = true
		case "--debug":
			debugFlag = true
		case "--web":
			webFlag = true
		case "--replace-existing":
			replaceExisting = true
		case "--web-port":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "`--web-port` requires a numeric value.")
				os.Exit(1)
			}
			i++
			port, err := strconv.Atoi(args[i])
			if err != nil || port <= 0 || port > 65535 {
				fmt.Fprintln(os.Stderr, "`--web-port` must be a valid TCP port (1-65535).")
				os.Exit(1)
			}
			webPortFlag = port
			webPortFlagSet = true
		default:
			payloadParts = append(payloadParts, arg)
		}
	}

	payload := strings.TrimSpace(strings.Join(payloadParts, " "))

	if hasPromptFlag && payload == "" {
		fmt.Fprintln(os.Stderr, "Usage: claudeclaw start --prompt <prompt> [--trigger] [--telegram] [--discord] [--debug] [--web] [--web-port <port>] [--replace-existing]")
		os.Exit(1)
	}
	if !hasPromptFlag && payload != "" {
		fmt.Fprintln(os.Stderr, "Prompt text requires `--prompt`.")
		os.Exit(1)
	}
	if telegramFlag && !hasTriggerFlag {
		fmt.Fprintln(os.Stderr, "`--telegram` with `start` requires `--trigger`.")
		os.Exit(1)
	}
	if discordFlag && !hasTriggerFlag {
		fmt.Fprintln(os.Stderr, "`--discord` with `start` requires `--trigger`.")
		os.Exit(1)
	}
	if hasPromptFlag && !hasTriggerFlag && (webFlag || webPortFlagSet) {
		fmt.Fprintln(os.Stderr, "`--web` is daemon-only. Remove `--prompt`, or add `--trigger`.")
		os.Exit(1)
	}

	// --- One-shot mode ---
	if hasPromptFlag && !hasTriggerFlag {
		existingPid, err := pid.CheckExistingDaemon()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error checking daemon: %v\n", err)
			os.Exit(1)
		}
		if existingPid != 0 {
			fmt.Fprintf(os.Stderr, "\x1b[31mAborted: daemon already running in this directory (PID %d)\x1b[0m\n", existingPid)
			fmt.Fprintln(os.Stderr, "Use `claudeclaw send <message> [--telegram] [--discord]` while daemon is running.")
			os.Exit(1)
		}

		if err := config.InitConfig(); err != nil {
			fmt.Fprintf(os.Stderr, "Config init error: %v\n", err)
			os.Exit(1)
		}
		if _, err := config.LoadSettings(); err != nil {
			fmt.Fprintf(os.Stderr, "Settings load error: %v\n", err)
			os.Exit(1)
		}
		if err := runner.EnsureProjectClaudeMd(); err != nil {
			fmt.Fprintf(os.Stderr, "CLAUDE.md error: %v\n", err)
			os.Exit(1)
		}

		result, err := runner.RunUserMessage(context.Background(), "prompt", payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Run error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result.Stdout)
		if result.ExitCode != 0 {
			os.Exit(result.ExitCode)
		}
		return
	}

	// --- Daemon mode ---
	existingPid, err := pid.CheckExistingDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking daemon: %v\n", err)
		os.Exit(1)
	}
	if existingPid != 0 {
		if !replaceExisting {
			fmt.Fprintf(os.Stderr, "\x1b[31mAborted: daemon already running in this directory (PID %d)\x1b[0m\n", existingPid)
			fmt.Fprintf(os.Stderr, "Use --stop first, or kill PID %d manually.\n", existingPid)
			os.Exit(1)
		}
		fmt.Printf("Replacing existing daemon (PID %d)...\n", existingPid)
		_ = syscall.Kill(existingPid, syscall.SIGTERM)
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			if err := syscall.Kill(existingPid, 0); err != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		_ = pid.CleanupPidFile()
	}

	if err := config.InitConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Config init error: %v\n", err)
		os.Exit(1)
	}
	settings, err := config.LoadSettings()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Settings load error: %v\n", err)
		os.Exit(1)
	}
	if err := runner.EnsureProjectClaudeMd(); err != nil {
		fmt.Fprintf(os.Stderr, "CLAUDE.md error: %v\n", err)
		os.Exit(1)
	}
	currentJobs, err := jobs.LoadJobs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Jobs load error: %v\n", err)
		os.Exit(1)
	}

	webEnabled := webFlag || webPortFlagSet || settings.Web.Enabled
	webPort := settings.Web.Port
	if webPortFlagSet {
		webPort = webPortFlag
	}

	if err := setupStatusline(); err != nil {
		fmt.Fprintf(os.Stderr, "Statusline setup error: %v\n", err)
		os.Exit(1)
	}
	if err := pid.WritePidFile(); err != nil {
		fmt.Fprintf(os.Stderr, "PID file error: %v\n", err)
		os.Exit(1)
	}

	// --- Context for graceful shutdown ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	var webHandle *web.Handle
	var discordStopOnce sync.Once

	go func() {
		<-sigCh
		if webHandle != nil {
			webHandle.Stop()
		}
		discordStopOnce.Do(func() {
			discord.StopGateway()
		})
		teardownStatusline()
		_ = pid.CleanupPidFile()
		cancel()
		os.Exit(0)
	}()

	fmt.Println("ClaudeClaw daemon started")
	fmt.Printf("  PID: %d\n", os.Getpid())
	fmt.Printf("  Security: %s\n", settings.Security.Level)
	if len(settings.Security.AllowedTools) > 0 {
		fmt.Printf("    + allowed: %s\n", strings.Join(settings.Security.AllowedTools, ", "))
	}
	if len(settings.Security.DisallowedTools) > 0 {
		fmt.Printf("    - blocked: %s\n", strings.Join(settings.Security.DisallowedTools, ", "))
	}
	if settings.Heartbeat.Enabled {
		fmt.Printf("  Heartbeat: every %dm\n", settings.Heartbeat.Interval)
	} else {
		fmt.Println("  Heartbeat: disabled")
	}
	if webEnabled {
		fmt.Printf("  Web UI: http://%s:%d\n", settings.Web.Host, webPort)
	} else {
		fmt.Println("  Web UI: disabled")
	}
	if debugFlag {
		fmt.Println("  Debug: enabled")
	}
	fmt.Printf("  Jobs loaded: %d\n", len(currentJobs))
	for _, j := range currentJobs {
		fmt.Printf("    - %s [%s]\n", j.Name, j.Schedule)
	}

	// --- Mutable state (protected by mu) ---
	var mu sync.Mutex
	currentSettings := settings
	daemonStartedAt := nowMs()
	var nextHeartbeatAt int64

	// --- Telegram init ---
	telegramToken := ""
	type telegramSendFunc func(chatID int, text string) error
	var telegramSend telegramSendFunc

	initTelegram := func(token string) {
		if token != "" && token != telegramToken {
			telegram.StartPolling(debugFlag)
			telegramToken = token
			telegramSend = func(chatID int, text string) error {
				return telegram.SendMessage(token, chatID, text, nil)
			}
			fmt.Printf("[%s] Telegram: enabled\n", ts())
		} else if token == "" && telegramToken != "" {
			telegramSend = nil
			telegramToken = ""
			fmt.Printf("[%s] Telegram: disabled\n", ts())
		}
	}

	initTelegram(currentSettings.Telegram.Token)
	if telegramToken == "" {
		fmt.Println("  Telegram: not configured")
	}

	// --- Discord init ---
	discordToken := ""
	type discordSendFunc func(userID, text string) error
	var discordSendToUser discordSendFunc

	initDiscord := func(token string) {
		if token != "" && token != discordToken {
			if discordToken != "" {
				discordStopOnce.Do(func() {
					discord.StopGateway()
				})
				discordStopOnce = sync.Once{}
			}
			discord.StartGateway(debugFlag)
			discordToken = token
			discordSendToUser = func(userID, text string) error {
				return discord.SendMessageToUser(token, userID, text)
			}
			fmt.Printf("[%s] Discord: enabled\n", ts())
		} else if token == "" && discordToken != "" {
			discordStopOnce.Do(func() {
				discord.StopGateway()
			})
			discordStopOnce = sync.Once{}
			discordSendToUser = nil
			discordToken = ""
			fmt.Printf("[%s] Discord: disabled\n", ts())
		}
	}

	initDiscord(currentSettings.Discord.Token)
	if discordToken == "" {
		fmt.Println("  Discord: not configured")
	}

	// --- Forward helpers ---
	forwardToTelegram := func(label string, result *runner.RunResult) {
		if telegramSend == nil || len(currentSettings.Telegram.AllowedUserIds) == 0 {
			return
		}
		var text string
		if result.ExitCode == 0 {
			if label != "" {
				text = fmt.Sprintf("[%s]\n%s", label, result.Stdout)
			} else {
				text = result.Stdout
			}
			if text == "" {
				text = "(empty)"
			}
		} else {
			stderr := result.Stderr
			if stderr == "" {
				stderr = "Unknown"
			}
			if label != "" {
				text = fmt.Sprintf("[%s] error (exit %d): %s", label, result.ExitCode, stderr)
			} else {
				text = fmt.Sprintf("error (exit %d): %s", result.ExitCode, stderr)
			}
		}
		for _, userID := range currentSettings.Telegram.AllowedUserIds {
			if err := telegramSend(userID, text); err != nil {
				fmt.Fprintf(os.Stderr, "[Telegram] Failed to forward to %d: %v\n", userID, err)
			}
		}
	}

	forwardToDiscord := func(label string, result *runner.RunResult) {
		if discordSendToUser == nil || len(currentSettings.Discord.AllowedUserIds) == 0 {
			return
		}
		var text string
		if result.ExitCode == 0 {
			if label != "" {
				text = fmt.Sprintf("[%s]\n%s", label, result.Stdout)
			} else {
				text = result.Stdout
			}
			if text == "" {
				text = "(empty)"
			}
		} else {
			stderr := result.Stderr
			if stderr == "" {
				stderr = "Unknown"
			}
			if label != "" {
				text = fmt.Sprintf("[%s] error (exit %d): %s", label, result.ExitCode, stderr)
			} else {
				text = fmt.Sprintf("error (exit %d): %s", result.ExitCode, stderr)
			}
		}
		for _, userID := range currentSettings.Discord.AllowedUserIds {
			if err := discordSendToUser(userID, text); err != nil {
				fmt.Fprintf(os.Stderr, "[Discord] Failed to forward to %s: %v\n", userID, err)
			}
		}
	}

	// --- State update ---
	updateState := func() {
		mu.Lock()
		s := currentSettings
		j := currentJobs
		hbAt := nextHeartbeatAt
		mu.Unlock()

		now := time.Now()
		jobStates := make([]statusline.JobState, len(j))
		for i, job := range j {
			jobStates[i] = statusline.JobState{
				Name:   job.Name,
				NextAt: cron.NextCronMatch(job.Schedule, now, s.TimezoneOffsetMinutes).UnixMilli(),
			}
		}

		var hbState *statusline.HeartbeatState
		if s.Heartbeat.Enabled {
			hbState = &statusline.HeartbeatState{NextAt: hbAt}
		}

		state := statusline.StateData{
			Heartbeat: hbState,
			Jobs:      jobStates,
			Security:  string(s.Security.Level),
			Telegram:  s.Telegram.Token != "",
			Discord:   s.Discord.Token != "",
			StartedAt: daemonStartedAt,
			Web: &statusline.WebState{
				Enabled: webHandle != nil,
				Host:    s.Web.Host,
				Port:    s.Web.Port,
			},
		}
		_ = statusline.WriteState(state)
	}

	// --- Heartbeat scheduling ---
	var heartbeatCancel context.CancelFunc

	scheduleHeartbeat := func() {
		if heartbeatCancel != nil {
			heartbeatCancel()
		}

		mu.Lock()
		s := currentSettings
		mu.Unlock()

		if !s.Heartbeat.Enabled {
			mu.Lock()
			nextHeartbeatAt = 0
			mu.Unlock()
			return
		}

		intervalMs := int64(s.Heartbeat.Interval) * 60 * 1000
		mu.Lock()
		nextHeartbeatAt = nextAllowedHeartbeatAt(
			s.Heartbeat,
			s.TimezoneOffsetMinutes,
			intervalMs,
			nowMs(),
		)
		mu.Unlock()

		hbCtx, hbCancel := context.WithCancel(ctx)
		heartbeatCancel = hbCancel

		go func() {
			for {
				mu.Lock()
				delayMs := nextHeartbeatAt - nowMs()
				mu.Unlock()

				if delayMs < 0 {
					delayMs = 0
				}
				timer := time.NewTimer(time.Duration(delayMs) * time.Millisecond)
				select {
				case <-hbCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}

				mu.Lock()
				s := currentSettings
				mu.Unlock()

				if isHeartbeatExcludedNow(s.Heartbeat, s.TimezoneOffsetMinutes) {
					fmt.Printf("[%s] Heartbeat skipped (excluded window)\n", ts())
					mu.Lock()
					nextHeartbeatAt = nextAllowedHeartbeatAt(
						s.Heartbeat,
						s.TimezoneOffsetMinutes,
						intervalMs,
						nowMs(),
					)
					mu.Unlock()
					continue
				}

				// Run heartbeat
				go func() {
					promptText, _ := config.ResolvePrompt(s.Heartbeat.Prompt)
					template, _ := runner.LoadHeartbeatPromptTemplate()

					var parts []string
					if strings.TrimSpace(template) != "" {
						parts = append(parts, strings.TrimSpace(template))
					}
					if strings.TrimSpace(promptText) != "" {
						parts = append(parts, "User custom heartbeat prompt:\n"+strings.TrimSpace(promptText))
					}
					mergedPrompt := strings.Join(parts, "\n\n")
					if mergedPrompt == "" {
						return
					}

					r, err := runner.Run(ctx, "heartbeat", mergedPrompt)
					if err != nil {
						fmt.Fprintf(os.Stderr, "[%s] Heartbeat error: %v\n", ts(), err)
						return
					}

					shouldForward := s.Heartbeat.ForwardToTelegram || !strings.HasPrefix(strings.TrimSpace(r.Stdout), "HEARTBEAT_OK")
					if shouldForward {
						forwardToTelegram("", r)
						forwardToDiscord("", r)
					}
				}()

				mu.Lock()
				nextHeartbeatAt = nextAllowedHeartbeatAt(
					s.Heartbeat,
					s.TimezoneOffsetMinutes,
					intervalMs,
					nowMs(),
				)
				mu.Unlock()
			}
		}()
	}

	// --- Web UI ---
	if webEnabled {
		mu.Lock()
		currentSettings.Web.Enabled = true
		mu.Unlock()

		handle, err := startWebWithFallback(
			currentSettings.Web.Host,
			webPort,
			func() web.Snapshot {
				mu.Lock()
				defer mu.Unlock()
				return web.Snapshot{
					PID:             os.Getpid(),
					StartedAt:       daemonStartedAt,
					HeartbeatNextAt: nextHeartbeatAt,
					Settings:        currentSettings,
					Jobs:            currentJobs,
				}
			},
			func(enabled bool) {
				mu.Lock()
				if currentSettings.Heartbeat.Enabled == enabled {
					mu.Unlock()
					return
				}
				currentSettings.Heartbeat.Enabled = enabled
				mu.Unlock()
				scheduleHeartbeat()
				updateState()
				fmt.Printf("[%s] Heartbeat %s from Web UI\n", ts(), map[bool]string{true: "enabled", false: "disabled"}[enabled])
			},
			func(patch web.HeartbeatPatch) {
				mu.Lock()
				changed := false
				if patch.Enabled != nil && currentSettings.Heartbeat.Enabled != *patch.Enabled {
					currentSettings.Heartbeat.Enabled = *patch.Enabled
					changed = true
				}
				if patch.Interval != nil {
					interval := *patch.Interval
					if interval < 1 {
						interval = 1
					}
					if interval > 1440 {
						interval = 1440
					}
					if currentSettings.Heartbeat.Interval != interval {
						currentSettings.Heartbeat.Interval = interval
						changed = true
					}
				}
				if patch.Prompt != nil && currentSettings.Heartbeat.Prompt != *patch.Prompt {
					currentSettings.Heartbeat.Prompt = *patch.Prompt
					changed = true
				}
				if patch.ExcludeWindows != nil {
					prevJSON, _ := json.Marshal(currentSettings.Heartbeat.ExcludeWindows)
					nextJSON, _ := json.Marshal(*patch.ExcludeWindows)
					if string(prevJSON) != string(nextJSON) {
						currentSettings.Heartbeat.ExcludeWindows = *patch.ExcludeWindows
						changed = true
					}
				}
				mu.Unlock()
				if !changed {
					return
				}
				scheduleHeartbeat()
				updateState()
				fmt.Printf("[%s] Heartbeat settings updated from Web UI\n", ts())
			},
			func() {
				newJobs, err := jobs.LoadJobs()
				if err != nil {
					fmt.Fprintf(os.Stderr, "[%s] Jobs reload error: %v\n", ts(), err)
					return
				}
				mu.Lock()
				currentJobs = newJobs
				mu.Unlock()
				scheduleHeartbeat()
				updateState()
				fmt.Printf("[%s] Jobs reloaded from Web UI\n", ts())
			},
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Web UI error: %v\n", err)
		} else {
			webHandle = handle
			mu.Lock()
			currentSettings.Web.Port = handle.Port
			mu.Unlock()
			fmt.Printf("[%s] Web UI listening on http://%s:%d\n", ts(), handle.Host, handle.Port)
		}
	}

	// --- Bootstrap / trigger ---
	if hasTriggerFlag {
		triggerPrompt := "Wake up, my friend!"
		if hasPromptFlag {
			triggerPrompt = payload
		}
		triggerResult, err := runner.Run(ctx, "trigger", triggerPrompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] Trigger error: %v\n", ts(), err)
		} else {
			fmt.Println(triggerResult.Stdout)
			if telegramFlag {
				forwardToTelegram("", triggerResult)
			}
			if discordFlag {
				forwardToDiscord("", triggerResult)
			}
			if triggerResult.ExitCode != 0 {
				fmt.Fprintf(os.Stderr, "[%s] Startup trigger failed (exit %d). Daemon will continue running.\n", ts(), triggerResult.ExitCode)
			}
		}
	} else {
		if err := runner.Bootstrap(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] Bootstrap error: %v\n", ts(), err)
		}
	}

	if currentSettings.Heartbeat.Enabled {
		scheduleHeartbeat()
	}

	// --- Initial state write ---
	updateState()

	// --- Hot-reload loop (every 30s) ---
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			func() {
				newSettings, err := config.ReloadSettings()
				if err != nil {
					fmt.Fprintf(os.Stderr, "[%s] Hot-reload error: %v\n", ts(), err)
					return
				}
				newJobs, err := jobs.LoadJobs()
				if err != nil {
					fmt.Fprintf(os.Stderr, "[%s] Jobs reload error: %v\n", ts(), err)
					return
				}

				mu.Lock()
				oldSettings := currentSettings
				mu.Unlock()

				// Detect heartbeat config changes
				hbChanged := newSettings.Heartbeat.Enabled != oldSettings.Heartbeat.Enabled ||
					newSettings.Heartbeat.Interval != oldSettings.Heartbeat.Interval ||
					newSettings.Heartbeat.Prompt != oldSettings.Heartbeat.Prompt ||
					newSettings.TimezoneOffsetMinutes != oldSettings.TimezoneOffsetMinutes ||
					newSettings.Timezone != oldSettings.Timezone

				if !hbChanged {
					oldEW, _ := json.Marshal(oldSettings.Heartbeat.ExcludeWindows)
					newEW, _ := json.Marshal(newSettings.Heartbeat.ExcludeWindows)
					hbChanged = string(oldEW) != string(newEW)
				}

				// Detect security config changes
				secChanged := newSettings.Security.Level != oldSettings.Security.Level ||
					strings.Join(newSettings.Security.AllowedTools, ",") != strings.Join(oldSettings.Security.AllowedTools, ",") ||
					strings.Join(newSettings.Security.DisallowedTools, ",") != strings.Join(oldSettings.Security.DisallowedTools, ",")

				if secChanged {
					fmt.Printf("[%s] Security level changed -> %s\n", ts(), newSettings.Security.Level)
				}

				if hbChanged {
					if newSettings.Heartbeat.Enabled {
						fmt.Printf("[%s] Config change detected -- heartbeat: every %dm\n", ts(), newSettings.Heartbeat.Interval)
					} else {
						fmt.Printf("[%s] Config change detected -- heartbeat: disabled\n", ts())
					}
				}

				mu.Lock()
				currentSettings = newSettings
				if webHandle != nil {
					currentSettings.Web.Enabled = true
					currentSettings.Web.Port = webHandle.Port
				}
				mu.Unlock()

				if hbChanged {
					scheduleHeartbeat()
				}

				// Detect job changes
				mu.Lock()
				oldJobs := currentJobs
				mu.Unlock()

				oldJobKey := jobsFingerprint(oldJobs)
				newJobKey := jobsFingerprint(newJobs)
				if oldJobKey != newJobKey {
					fmt.Printf("[%s] Jobs reloaded: %d job(s)\n", ts(), len(newJobs))
					for _, j := range newJobs {
						fmt.Printf("    - %s [%s]\n", j.Name, j.Schedule)
					}
				}
				mu.Lock()
				currentJobs = newJobs
				mu.Unlock()

				// Telegram/Discord changes
				initTelegram(newSettings.Telegram.Token)
				initDiscord(newSettings.Discord.Token)
			}()
		}
	}()

	// --- Cron tick (every 60s) ---
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			now := time.Now()
			mu.Lock()
			s := currentSettings
			j := make([]jobs.Job, len(currentJobs))
			copy(j, currentJobs)
			mu.Unlock()

			for _, job := range j {
				if cron.CronMatches(job.Schedule, now, s.TimezoneOffsetMinutes) {
					go func(job jobs.Job) {
						promptText, _ := config.ResolvePrompt(job.Prompt)
						r, err := runner.Run(ctx, job.Name, promptText)
						if err != nil {
							fmt.Fprintf(os.Stderr, "[%s] Job %s error: %v\n", ts(), job.Name, err)
							return
						}

						if job.Notify != jobs.NotifyFalse {
							if job.Notify != jobs.NotifyError || r.ExitCode != 0 {
								forwardToTelegram(job.Name, r)
								forwardToDiscord(job.Name, r)
							}
						}

						if !job.Recurring {
							if err := jobs.ClearJobSchedule(job.Name); err != nil {
								fmt.Fprintf(os.Stderr, "[%s] Failed to clear schedule for %s: %v\n", ts(), job.Name, err)
							} else {
								fmt.Printf("[%s] Cleared schedule for one-time job: %s\n", ts(), job.Name)
							}
						}
					}(job)
				}
			}
			updateState()
		}
	}()

	// Block until context is cancelled
	<-ctx.Done()
}

func jobsFingerprint(jobList []jobs.Job) string {
	parts := make([]string, len(jobList))
	for i, j := range jobList {
		parts[i] = fmt.Sprintf("%s:%s:%s", j.Name, j.Schedule, j.Prompt)
	}
	// Sort for stable comparison
	sorted := make([]string, len(parts))
	copy(sorted, parts)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return strings.Join(sorted, "|")
}

func startWebWithFallback(
	host string,
	preferredPort int,
	getSnapshot func() web.Snapshot,
	onHbEnabled func(bool),
	onHbSettings func(web.HeartbeatPatch),
	onJobsChanged func(),
) (*web.ServerHandle, error) {
	maxAttempts := 10
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		candidatePort := preferredPort + i
		handle, err := web.StartWebUI(web.Options{
			Host:                       host,
			Port:                       candidatePort,
			GetSnapshot:                getSnapshot,
			OnHeartbeatEnabledChanged:  onHbEnabled,
			OnHeartbeatSettingsChanged: onHbSettings,
			OnJobsChanged:              onJobsChanged,
		})
		if err == nil {
			return handle, nil
		}
		lastErr = err
		if !isAddrInUse(err) || i == maxAttempts-1 {
			return nil, err
		}
	}
	return nil, lastErr
}

func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "address already in use") ||
		strings.Contains(err.Error(), "EADDRINUSE")
}
