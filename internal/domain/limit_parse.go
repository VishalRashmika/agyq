package domain

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	relTokenRe = regexp.MustCompile(`(?i)(\d+)\s*(d(?:ay)?s?|h(?:(?:ou)?r)?s?|m(?:in(?:ute)?)?s?|s(?:ec(?:ond)?)?s?)`)
	timeOnlyRe = regexp.MustCompile(`(?i)^(\d{1,2}):(\d{2})(?::(\d{2}))?\s*(am|pm)?$`)
)

// ParseResetTime parses absolute or relative reset times from raw text according to LimitPatterns.
// Result is converted to UTC and rejected if in the past or > 8 days out.
func ParseResetTime(raw string, now time.Time, p LimitPatterns) *time.Time {
	if raw == "" {
		return nil
	}

	tz := p.Timezone
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}

	for _, pattern := range p.ResetRegexes {
		if pattern == "" {
			continue
		}
		// Prevent single-letter units like 'h' from shadowing 'hours' in regex alternations
		pattern = strings.ReplaceAll(pattern, "d|day|days|h|hr|hrs|hour|hours|m|min|mins|minute|minutes", "days|day|d|hours|hour|hrs|hr|h|minutes|minute|mins|min|m")
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}

		matches := re.FindStringSubmatch(raw)
		if matches == nil {
			continue
		}

		subexpNames := re.SubexpNames()
		var absStr, relStr string

		for i, name := range subexpNames {
			if i >= len(matches) {
				continue
			}
			if name == "abs" && matches[i] != "" {
				absStr = matches[i]
			} else if name == "rel" && matches[i] != "" {
				relStr = matches[i]
			}
		}

		// Fallback if no named groups
		if absStr == "" && relStr == "" && len(matches) > 1 {
			candidate := strings.TrimSpace(matches[1])
			if relTokenRe.MatchString(candidate) {
				relStr = candidate
			} else {
				absStr = candidate
			}
		}

		var parsed *time.Time
		if relStr != "" {
			parsed = parseRelativeDuration(relStr, now)
		} else if absStr != "" {
			parsed = parseAbsoluteTime(absStr, now, loc)
		}

		if parsed != nil {
			// Bounds check: reject results in the past or > 8 days out
			utc := parsed.UTC()
			if !utc.After(now) {
				continue
			}
			if utc.Sub(now) > 8*24*time.Hour {
				continue
			}
			return &utc
		}
	}

	return nil
}

func parseRelativeDuration(rel string, now time.Time) *time.Time {
	tokens := relTokenRe.FindAllStringSubmatch(rel, -1)
	if len(tokens) == 0 {
		return nil
	}

	var total time.Duration
	for _, tok := range tokens {
		if len(tok) < 3 {
			continue
		}
		val, err := strconv.Atoi(tok[1])
		if err != nil || val < 0 {
			continue
		}
		unit := strings.ToLower(tok[2])
		switch {
		case strings.HasPrefix(unit, "d"):
			total += time.Duration(val) * 24 * time.Hour
		case strings.HasPrefix(unit, "h"):
			total += time.Duration(val) * time.Hour
		case strings.HasPrefix(unit, "m"):
			total += time.Duration(val) * time.Minute
		case strings.HasPrefix(unit, "s"):
			total += time.Duration(val) * time.Second
		}
	}

	if total <= 0 {
		return nil
	}
	t := now.Add(total).UTC()
	return &t
}

func parseAbsoluteTime(abs string, now time.Time, loc *time.Location) *time.Time {
	cleaned := strings.TrimSpace(abs)
	cleaned = strings.TrimRight(cleaned, ".,;!")

	// 1. Try RFC3339 / ISO formats
	rfcFormats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, layout := range rfcFormats {
		if t, err := time.ParseInLocation(layout, cleaned, loc); err == nil {
			utc := t.UTC()
			return &utc
		}
	}

	nowInLoc := now.In(loc)
	lower := strings.ToLower(cleaned)

	// 2. Handle "tomorrow at 14:35" or "tomorrow 14:35"
	if strings.Contains(lower, "tomorrow") {
		timePart := strings.TrimSpace(strings.ReplaceAll(lower, "tomorrow", ""))
		timePart = strings.TrimPrefix(timePart, "at")
		timePart = strings.TrimSpace(timePart)
		if hour, min, sec, ok := parseTimeComponents(timePart); ok {
			candidate := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day()+1, hour, min, sec, 0, loc)
			utc := candidate.UTC()
			return &utc
		}
	}

	// 3. Handle weekday: e.g. "Sunday at 15:00" or "Sunday"
	weekdays := map[string]time.Weekday{
		"sunday":    time.Sunday,
		"monday":    time.Monday,
		"tuesday":   time.Tuesday,
		"wednesday": time.Wednesday,
		"thursday":  time.Thursday,
		"friday":    time.Friday,
		"saturday":  time.Saturday,
	}
	for name, targetWd := range weekdays {
		if strings.Contains(lower, name) {
			timePart := strings.TrimSpace(strings.ReplaceAll(lower, name, ""))
			timePart = strings.TrimPrefix(timePart, "at")
			timePart = strings.TrimSpace(timePart)
			hour, min, sec, hasTime := parseTimeComponents(timePart)
			if !hasTime {
				hour, min, sec = 0, 0, 0
			}

			daysAhead := (int(targetWd) - int(nowInLoc.Weekday()) + 7) % 7
			candidate := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day()+daysAhead, hour, min, sec, 0, loc)
			if daysAhead == 0 && (candidate.Before(nowInLoc) || candidate.Equal(nowInLoc)) {
				// Target is later next week
				candidate = candidate.AddDate(0, 0, 7)
			}
			utc := candidate.UTC()
			return &utc
		}
	}

	// 4. Time of day only: e.g. "14:35", "14:35:00", "2:35 PM"
	if hour, min, sec, ok := parseTimeComponents(lower); ok {
		candidate := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), hour, min, sec, 0, loc)
		// Day rollover: if time already passed today in loc, rollover to tomorrow
		if candidate.Before(nowInLoc) || candidate.Equal(nowInLoc) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		utc := candidate.UTC()
		return &utc
	}

	return nil
}

func parseTimeComponents(s string) (hour, min, sec int, ok bool) {
	s = strings.TrimSpace(s)
	matches := timeOnlyRe.FindStringSubmatch(s)
	if matches == nil {
		return 0, 0, 0, false
	}

	h, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, 0, 0, false
	}
	m, err := strconv.Atoi(matches[2])
	if err != nil {
		return 0, 0, 0, false
	}
	if matches[3] != "" {
		sec, _ = strconv.Atoi(matches[3])
	}

	ampm := strings.ToLower(matches[4])
	if ampm == "pm" && h < 12 {
		h += 12
	} else if ampm == "am" && h == 12 {
		h = 0
	}

	if h < 0 || h > 23 || m < 0 || m > 59 || sec < 0 || sec > 59 {
		return 0, 0, 0, false
	}

	return h, m, sec, true
}
