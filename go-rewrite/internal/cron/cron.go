// Package cron provides cron expression matching and next-match calculation.
package cron

import (
	"strconv"
	"strings"
	"time"

	"github.com/claudeclaw/claudeclaw/internal/timezone"
)

// matchCronField checks if a single cron field (e.g. "*/5", "1-3", "1,2,3") matches the given value.
func matchCronField(field string, value int) bool {
	for _, part := range strings.Split(field, ",") {
		pieces := strings.SplitN(part, "/", 2)
		rangePart := pieces[0]
		step := 1
		if len(pieces) == 2 {
			s, err := strconv.Atoi(pieces[1])
			if err == nil && s > 0 {
				step = s
			}
		}

		if rangePart == "*" {
			if value%step == 0 {
				return true
			}
			continue
		}

		if strings.Contains(rangePart, "-") {
			bounds := strings.SplitN(rangePart, "-", 2)
			lo, err1 := strconv.Atoi(bounds[0])
			hi, err2 := strconv.Atoi(bounds[1])
			if err1 == nil && err2 == nil {
				if value >= lo && value <= hi && (value-lo)%step == 0 {
					return true
				}
			}
			continue
		}

		n, err := strconv.Atoi(rangePart)
		if err == nil && n == value {
			return true
		}
	}
	return false
}

// CronMatches checks if the given time matches the cron expression (5-field: min hour dom month dow).
// The time is shifted by tzOffsetMinutes before matching.
func CronMatches(expr string, t time.Time, tzOffsetMinutes int) bool {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) < 5 {
		return false
	}

	shifted := timezone.ShiftDateToOffset(t, tzOffsetMinutes)

	minute := shifted.Minute()
	hour := shifted.Hour()
	dayOfMonth := shifted.Day()
	month := int(shifted.Month())
	dayOfWeek := int(shifted.Weekday())

	return matchCronField(fields[0], minute) &&
		matchCronField(fields[1], hour) &&
		matchCronField(fields[2], dayOfMonth) &&
		matchCronField(fields[3], month) &&
		matchCronField(fields[4], dayOfWeek)
}

// NextCronMatch finds the next time after `after` that matches the cron expression.
// It searches up to 2880 minutes (48 hours) ahead.
func NextCronMatch(expr string, after time.Time, tzOffsetMinutes int) time.Time {
	d := after.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 2880; i++ {
		if CronMatches(expr, d, tzOffsetMinutes) {
			return d
		}
		d = d.Add(time.Minute)
	}
	return d
}
