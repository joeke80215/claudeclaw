// Package config manages settings loaded from .claude/claudeclaw/settings.json.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/claudeclaw/claudeclaw/internal/timezone"
)

// SecurityLevel represents a security configuration level.
type SecurityLevel string

const (
	SecurityLocked       SecurityLevel = "locked"
	SecurityStrict       SecurityLevel = "strict"
	SecurityModerate     SecurityLevel = "moderate"
	SecurityUnrestricted SecurityLevel = "unrestricted"
)

// HeartbeatExcludeWindow defines a time window during which heartbeat is suppressed.
type HeartbeatExcludeWindow struct {
	Days  []int  `json:"days,omitempty"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// HeartbeatConfig holds heartbeat scheduling configuration.
type HeartbeatConfig struct {
	Enabled           bool                     `json:"enabled"`
	Interval          int                      `json:"interval"`
	Prompt            string                   `json:"prompt"`
	ExcludeWindows    []HeartbeatExcludeWindow `json:"excludeWindows"`
	ForwardToTelegram bool                     `json:"forwardToTelegram"`
}

// TelegramConfig holds Telegram bot configuration.
type TelegramConfig struct {
	Token          string  `json:"token"`
	AllowedUserIds []int64 `json:"allowedUserIds"`
	BaseURL        string  `json:"baseUrl"`
}

// DiscordConfig holds Discord bot configuration.
// AllowedUserIds are strings because Discord snowflake IDs exceed 2^53.
type DiscordConfig struct {
	Token          string   `json:"token"`
	AllowedUserIds []string `json:"allowedUserIds"`
	ListenChannels []string `json:"listenChannels"`
}

// SecurityConfig holds security level and tool restrictions.
type SecurityConfig struct {
	Level           SecurityLevel `json:"level"`
	AllowedTools    []string      `json:"allowedTools"`
	DisallowedTools []string      `json:"disallowedTools"`
}

// ModelConfig holds model name and API key/URL.
type ModelConfig struct {
	Model string `json:"model"`
	API   string `json:"api"`
}

// WebConfig holds web dashboard configuration.
type WebConfig struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
}

// SttConfig holds speech-to-text API configuration.
type SttConfig struct {
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
}

// Settings represents the full settings.json configuration.
type Settings struct {
	Model                 string          `json:"model"`
	API                   string          `json:"api"`
	Fallback              ModelConfig     `json:"fallback"`
	Timezone              string          `json:"timezone"`
	TimezoneOffsetMinutes int             `json:"timezoneOffsetMinutes"`
	Heartbeat             HeartbeatConfig `json:"heartbeat"`
	Telegram              TelegramConfig  `json:"telegram"`
	Discord               DiscordConfig   `json:"discord"`
	Security              SecurityConfig  `json:"security"`
	Web                   WebConfig       `json:"web"`
	STT                   SttConfig       `json:"stt"`
}

var (
	mu     sync.RWMutex
	cached *Settings
)

var validSecurityLevels = map[SecurityLevel]bool{
	SecurityLocked:       true,
	SecurityStrict:       true,
	SecurityModerate:     true,
	SecurityUnrestricted: true,
}

// BaseDir returns the base directory for claudeclaw state files.
func BaseDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return filepath.Join(cwd, ".claude", "claudeclaw")
}

// SettingsPath returns the path to settings.json.
func SettingsPath() string {
	return filepath.Join(BaseDir(), "settings.json")
}

// JobsDir returns the path to the jobs directory.
func JobsDir() string {
	return filepath.Join(BaseDir(), "jobs")
}

// LogsDir returns the path to the logs directory.
func LogsDir() string {
	return filepath.Join(BaseDir(), "logs")
}

func defaultSettings() *Settings {
	return &Settings{
		Model:                 "",
		API:                   "",
		Fallback:              ModelConfig{Model: "", API: ""},
		Timezone:              "UTC",
		TimezoneOffsetMinutes: 0,
		Heartbeat: HeartbeatConfig{
			Enabled:           false,
			Interval:          15,
			Prompt:            "",
			ExcludeWindows:    []HeartbeatExcludeWindow{},
			ForwardToTelegram: true,
		},
		Telegram: TelegramConfig{Token: "", AllowedUserIds: []int64{}},
		Discord:  DiscordConfig{Token: "", AllowedUserIds: []string{}, ListenChannels: []string{}},
		Security: SecurityConfig{Level: SecurityModerate, AllowedTools: []string{}, DisallowedTools: []string{}},
		Web:      WebConfig{Enabled: false, Host: "127.0.0.1", Port: 4632},
		STT:      SttConfig{BaseURL: "", Model: ""},
	}
}

// InitConfig creates the config directories and default settings file if they don't exist.
func InitConfig() error {
	base := BaseDir()
	for _, dir := range []string{base, JobsDir(), LogsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("config: mkdir %s: %w", dir, err)
		}
	}
	path := SettingsPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		data, err := json.MarshalIndent(defaultSettings(), "", "  ")
		if err != nil {
			return fmt.Errorf("config: marshal defaults: %w", err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			return fmt.Errorf("config: write %s: %w", path, err)
		}
	}
	return nil
}

var timeRe = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

func parseExcludeWindows(raw []interface{}) []HeartbeatExcludeWindow {
	allDays := []int{0, 1, 2, 3, 4, 5, 6}
	var out []HeartbeatExcludeWindow
	for _, entry := range raw {
		m, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		start, _ := m["start"].(string)
		end, _ := m["end"].(string)
		start = strings.TrimSpace(start)
		end = strings.TrimSpace(end)
		if !timeRe.MatchString(start) || !timeRe.MatchString(end) {
			continue
		}

		var parsedDays []int
		if rawDays, ok := m["days"].([]interface{}); ok {
			seen := map[int]bool{}
			for _, d := range rawDays {
				var n int
				switch v := d.(type) {
				case float64:
					n = int(v)
				case json.Number:
					nn, err := v.Int64()
					if err != nil {
						continue
					}
					n = int(nn)
				default:
					continue
				}
				if n >= 0 && n <= 6 && !seen[n] {
					seen[n] = true
					parsedDays = append(parsedDays, n)
				}
			}
			sort.Ints(parsedDays)
		}

		days := parsedDays
		if len(days) == 0 {
			days = make([]int, len(allDays))
			copy(days, allDays)
		}

		out = append(out, HeartbeatExcludeWindow{
			Start: start,
			End:   end,
			Days:  days,
		})
	}
	if out == nil {
		out = []HeartbeatExcludeWindow{}
	}
	return out
}

// extractDiscordUserIds extracts discord.allowedUserIds as raw strings from JSON text
// to preserve precision for snowflake IDs > 2^53.
var discordBlockRe = regexp.MustCompile(`"discord"\s*:\s*\{[\s\S]*?\}`)
var discordUserIdsRe = regexp.MustCompile(`"allowedUserIds"\s*:\s*\[([\s\S]*?)\]`)
var discordIdValueRe = regexp.MustCompile(`"(\d+)"|(\d+)`)

func extractDiscordUserIds(rawText string) []string {
	block := discordBlockRe.FindString(rawText)
	if block == "" {
		return nil
	}
	arrayMatch := discordUserIdsRe.FindStringSubmatch(block)
	if arrayMatch == nil {
		return nil
	}
	var items []string
	for _, m := range discordIdValueRe.FindAllStringSubmatch(arrayMatch[1], -1) {
		if m[1] != "" {
			items = append(items, m[1])
		} else if m[2] != "" {
			items = append(items, m[2])
		}
	}
	return items
}

func parseSettings(raw map[string]interface{}, discordUserIds []string) *Settings {
	s := defaultSettings()

	if v, ok := raw["model"].(string); ok {
		s.Model = strings.TrimSpace(v)
	}
	if v, ok := raw["api"].(string); ok {
		s.API = strings.TrimSpace(v)
	}

	if fb, ok := raw["fallback"].(map[string]interface{}); ok {
		if v, ok := fb["model"].(string); ok {
			s.Fallback.Model = strings.TrimSpace(v)
		}
		if v, ok := fb["api"].(string); ok {
			s.Fallback.API = strings.TrimSpace(v)
		}
	}

	if v, ok := raw["timezone"]; ok {
		if str, ok := v.(string); ok {
			s.Timezone = timezone.NormalizeTimezoneName(str)
		}
	}
	if s.Timezone == "" {
		s.Timezone = "UTC"
	}

	s.TimezoneOffsetMinutes = timezone.ResolveTimezoneOffsetMinutes(raw["timezoneOffsetMinutes"], s.Timezone)

	if hb, ok := raw["heartbeat"].(map[string]interface{}); ok {
		if v, ok := hb["enabled"].(bool); ok {
			s.Heartbeat.Enabled = v
		}
		if v, ok := hb["interval"].(float64); ok {
			s.Heartbeat.Interval = int(v)
		}
		if v, ok := hb["prompt"].(string); ok {
			s.Heartbeat.Prompt = v
		}
		if v, ok := hb["excludeWindows"].([]interface{}); ok {
			s.Heartbeat.ExcludeWindows = parseExcludeWindows(v)
		}
		if v, ok := hb["forwardToTelegram"].(bool); ok {
			s.Heartbeat.ForwardToTelegram = v
		}
	}

	if tg, ok := raw["telegram"].(map[string]interface{}); ok {
		if v, ok := tg["token"].(string); ok {
			s.Telegram.Token = v
		}
		if v, ok := tg["allowedUserIds"].([]interface{}); ok {
			ids := make([]int64, 0, len(v))
			for _, item := range v {
				if n, ok := item.(float64); ok {
					ids = append(ids, int64(n))
				}
			}
			s.Telegram.AllowedUserIds = ids
		}
		if v, ok := tg["baseUrl"].(string); ok {
			s.Telegram.BaseURL = strings.TrimSpace(v)
		}
	}

	if dc, ok := raw["discord"].(map[string]interface{}); ok {
		if v, ok := dc["token"].(string); ok {
			s.Discord.Token = strings.TrimSpace(v)
		}
		if len(discordUserIds) > 0 {
			s.Discord.AllowedUserIds = discordUserIds
		} else if v, ok := dc["allowedUserIds"].([]interface{}); ok {
			ids := make([]string, 0, len(v))
			for _, item := range v {
				ids = append(ids, fmt.Sprintf("%v", item))
			}
			s.Discord.AllowedUserIds = ids
		}
		if v, ok := dc["listenChannels"].([]interface{}); ok {
			chs := make([]string, 0, len(v))
			for _, item := range v {
				chs = append(chs, fmt.Sprintf("%v", item))
			}
			s.Discord.ListenChannels = chs
		}
	}

	if sec, ok := raw["security"].(map[string]interface{}); ok {
		if v, ok := sec["level"].(string); ok {
			level := SecurityLevel(v)
			if validSecurityLevels[level] {
				s.Security.Level = level
			}
		}
		if v, ok := sec["allowedTools"].([]interface{}); ok {
			tools := make([]string, 0, len(v))
			for _, item := range v {
				if str, ok := item.(string); ok {
					tools = append(tools, str)
				}
			}
			s.Security.AllowedTools = tools
		}
		if v, ok := sec["disallowedTools"].([]interface{}); ok {
			tools := make([]string, 0, len(v))
			for _, item := range v {
				if str, ok := item.(string); ok {
					tools = append(tools, str)
				}
			}
			s.Security.DisallowedTools = tools
		}
	}

	if web, ok := raw["web"].(map[string]interface{}); ok {
		if v, ok := web["enabled"].(bool); ok {
			s.Web.Enabled = v
		}
		if v, ok := web["host"].(string); ok {
			s.Web.Host = v
		}
		if v, ok := web["port"].(float64); ok {
			s.Web.Port = int(v)
		}
	}

	if stt, ok := raw["stt"].(map[string]interface{}); ok {
		if v, ok := stt["baseUrl"].(string); ok {
			s.STT.BaseURL = strings.TrimSpace(v)
		}
		if v, ok := stt["model"].(string); ok {
			s.STT.Model = strings.TrimSpace(v)
		}
	}

	return s
}

// LoadSettings loads settings from disk. Returns cached settings if already loaded.
func LoadSettings() (*Settings, error) {
	mu.Lock()
	defer mu.Unlock()

	if cached != nil {
		return cached, nil
	}
	return loadFromDisk()
}

// ReloadSettings forces a reload of settings from disk, bypassing cache.
func ReloadSettings() (*Settings, error) {
	mu.Lock()
	defer mu.Unlock()
	return loadFromDisk()
}

func loadFromDisk() (*Settings, error) {
	rawText, err := os.ReadFile(SettingsPath())
	if err != nil {
		return nil, fmt.Errorf("config: read settings: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(rawText, &raw); err != nil {
		return nil, fmt.Errorf("config: parse settings: %w", err)
	}

	discordUserIds := extractDiscordUserIds(string(rawText))
	cached = parseSettings(raw, discordUserIds)
	return cached, nil
}

// GetSettings returns the cached settings. Panics if settings have not been loaded.
func GetSettings() *Settings {
	mu.RLock()
	defer mu.RUnlock()
	if cached == nil {
		panic("config: settings not loaded — call LoadSettings() first")
	}
	return cached
}

var promptExtensions = []string{".md", ".txt", ".prompt"}

// ResolvePrompt resolves a prompt string. If it ends with .md, .txt, or .prompt,
// it reads the file contents. Relative paths are resolved from cwd.
func ResolvePrompt(prompt string) (string, error) {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return trimmed, nil
	}

	isPath := false
	for _, ext := range promptExtensions {
		if strings.HasSuffix(trimmed, ext) {
			isPath = true
			break
		}
	}
	if !isPath {
		return trimmed, nil
	}

	resolved := trimmed
	if !filepath.IsAbs(trimmed) {
		cwd, err := os.Getwd()
		if err != nil {
			return trimmed, nil
		}
		resolved = filepath.Join(cwd, trimmed)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		// File not found — use as literal string (matching TS behavior)
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "[config] Prompt path %q not found, using as literal string\n", trimmed)
			return trimmed, nil
		}
		return trimmed, nil
	}
	return strings.TrimSpace(string(data)), nil
}

// HasModelConfig returns true if a ModelConfig has a non-empty model or api.
func HasModelConfig(mc ModelConfig) bool {
	return strings.TrimSpace(mc.Model) != "" || strings.TrimSpace(mc.API) != ""
}

// SameModelConfig returns true if two ModelConfigs refer to the same model+api.
func SameModelConfig(a, b ModelConfig) bool {
	return strings.EqualFold(strings.TrimSpace(a.Model), strings.TrimSpace(b.Model)) &&
		strings.TrimSpace(a.API) == strings.TrimSpace(b.API)
}

// SetCachedForTest allows tests to inject settings. Not for production use.
func SetCachedForTest(s *Settings) {
	mu.Lock()
	defer mu.Unlock()
	cached = s
}

// PromptsDir returns the path to the prompts/ directory relative to the binary
// or working directory. This resolves to the project-level prompts dir.
func PromptsDir() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, "prompts")
}

// ProjectPromptsDir returns the path for project-level prompt overrides.
func ProjectPromptsDir() string {
	return filepath.Join(BaseDir(), "prompts")
}

// IntToStr is a helper for formatting int to string.
func IntToStr(n int) string {
	return strconv.Itoa(n)
}
