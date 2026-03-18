// Package timezone provides timezone offset manipulation, formatting, and clock prefix generation.
package timezone

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	MinOffsetMinutes = -12 * 60 // -720
	MaxOffsetMinutes = 14 * 60  // 840
)

func pad2(v int) string {
	if v < 10 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}

// ClampTimezoneOffsetMinutes clamps the given offset to the valid range [-720, 840].
func ClampTimezoneOffsetMinutes(value int) int {
	if value < MinOffsetMinutes {
		return MinOffsetMinutes
	}
	if value > MaxOffsetMinutes {
		return MaxOffsetMinutes
	}
	return value
}

var utcOffsetRe = regexp.MustCompile(`^(UTC|GMT)([+-])(\d{1,2})(?::?([0-5]\d))?$`)

// ParseUtcOffsetMinutes parses strings like "UTC+8", "GMT-5:30", "UTC", "GMT".
// Returns the offset in minutes and true if successful, or 0 and false otherwise.
func ParseUtcOffsetMinutes(value string) (int, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, " ", "")
	if normalized == "UTC" || normalized == "GMT" {
		return 0, true
	}
	m := utcOffsetRe.FindStringSubmatch(normalized)
	if m == nil {
		return 0, false
	}
	sign := 1
	if m[2] == "-" {
		sign = -1
	}
	hours, _ := strconv.Atoi(m[3])
	minutes := 0
	if m[4] != "" {
		minutes, _ = strconv.Atoi(m[4])
	}
	if hours > 14 {
		return 0, false
	}
	total := sign * (hours*60 + minutes)
	if total < MinOffsetMinutes || total > MaxOffsetMinutes {
		return 0, false
	}
	return total, true
}

// NormalizeTimezoneName validates a timezone name. If it's a UTC/GMT offset string,
// it normalizes it. If it's a valid IANA timezone, it returns it as-is. Otherwise returns "".
func NormalizeTimezoneName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if _, ok := ParseUtcOffsetMinutes(trimmed); ok {
		return strings.ToUpper(strings.ReplaceAll(trimmed, " ", ""))
	}
	_, err := time.LoadLocation(trimmed)
	if err != nil {
		return ""
	}
	return trimmed
}

// getCurrentOffsetMinutesForIANA returns the current UTC offset in minutes for a named IANA timezone.
func getCurrentOffsetMinutesForIANA(tz string) (int, bool) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return 0, false
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return 0, false
	}
	_, offset := time.Now().In(loc).Zone()
	return ClampTimezoneOffsetMinutes(offset / 60), true
}

// ResolveTimezoneOffsetMinutes resolves a timezone offset from a value that may be
// an int, float64, string number, or falls back to parsing the fallback timezone name.
func ResolveTimezoneOffsetMinutes(value interface{}, fallback string) int {
	switch v := value.(type) {
	case int:
		return ClampTimezoneOffsetMinutes(v)
	case float64:
		if !math.IsInf(v, 0) && !math.IsNaN(v) {
			return ClampTimezoneOffsetMinutes(int(math.Round(v)))
		}
	case string:
		v = strings.TrimSpace(v)
		if n, err := strconv.ParseFloat(v, 64); err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
			return ClampTimezoneOffsetMinutes(int(math.Round(n)))
		}
	}
	if offset, ok := ParseUtcOffsetMinutes(fallback); ok {
		return offset
	}
	if offset, ok := getCurrentOffsetMinutesForIANA(fallback); ok {
		return offset
	}
	return 0
}

// ShiftDateToOffset shifts a time.Time by the given offset minutes from UTC.
// The returned time is in UTC but represents the wall clock at the given offset.
func ShiftDateToOffset(t time.Time, offsetMinutes int) time.Time {
	clamped := ClampTimezoneOffsetMinutes(offsetMinutes)
	return t.UTC().Add(time.Duration(clamped) * time.Minute)
}

// FormatUtcOffsetLabel formats an offset like "UTC+8" or "UTC-5:30".
func FormatUtcOffsetLabel(offsetMinutes int) string {
	clamped := ClampTimezoneOffsetMinutes(offsetMinutes)
	sign := "+"
	if clamped < 0 {
		sign = "-"
	}
	abs := clamped
	if abs < 0 {
		abs = -abs
	}
	hours := abs / 60
	minutes := abs % 60
	if minutes == 0 {
		return fmt.Sprintf("UTC%s%d", sign, hours)
	}
	return fmt.Sprintf("UTC%s%d:%s", sign, hours, pad2(minutes))
}

// BuildClockPromptPrefix builds a string like "[2024-01-15 14:30:00 UTC+8]".
func BuildClockPromptPrefix(t time.Time, offsetMinutes int) string {
	shifted := ShiftDateToOffset(t, offsetMinutes)
	label := FormatUtcOffsetLabel(offsetMinutes)
	timestamp := fmt.Sprintf("%04d-%s-%s %s:%s:%s",
		shifted.Year(),
		pad2(int(shifted.Month())),
		pad2(shifted.Day()),
		pad2(shifted.Hour()),
		pad2(shifted.Minute()),
		pad2(shifted.Second()),
	)
	return fmt.Sprintf("[%s %s]", timestamp, label)
}

// GetDayAndMinuteAtOffset returns the day of week (0=Sunday) and minute-of-day
// for the given time shifted by the offset.
func GetDayAndMinuteAtOffset(t time.Time, offsetMinutes int) (day int, minute int) {
	shifted := ShiftDateToOffset(t, offsetMinutes)
	return int(shifted.Weekday()), shifted.Hour()*60 + shifted.Minute()
}
