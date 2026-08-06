package handler

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type reminderCadenceKind string

const (
	reminderCadenceEvery  reminderCadenceKind = "every"
	reminderCadenceDaily  reminderCadenceKind = "daily"
	reminderCadenceWeekly reminderCadenceKind = "weekly"
)

type reminderCadence struct {
	Canonical string
	Kind      reminderCadenceKind
	Interval  time.Duration
	Hour      int
	Minute    int
	Weekdays  []time.Weekday
	Location  *time.Location
}

type reminderSchedule struct {
	FireAt      time.Time
	Cadence     *reminderCadence
	Timezone    string
	CadenceSlot *time.Time
}

func parseReminderSchedule(now time.Time, delaySeconds *int64, rawFireAt, rawRepeat, timezone string) (reminderSchedule, error) {
	hasOneShot := delaySeconds != nil || strings.TrimSpace(rawFireAt) != ""
	hasRepeat := strings.TrimSpace(rawRepeat) != ""
	if hasOneShot == hasRepeat {
		return reminderSchedule{}, fmt.Errorf("provide exactly one of delay_seconds, fire_at, or repeat")
	}
	if hasOneShot {
		fireAt, err := parseReminderFireAt(now, delaySeconds, rawFireAt)
		if err != nil {
			return reminderSchedule{}, err
		}
		return reminderSchedule{FireAt: fireAt}, nil
	}

	calendarRule := strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawRepeat)), "daily@") ||
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawRepeat)), "weekly:")
	if !calendarRule {
		timezone = ""
	}
	cadence, err := parseReminderCadence(rawRepeat, timezone)
	if err != nil {
		return reminderSchedule{}, err
	}
	next, err := nextReminderCadence(cadence, now)
	if err != nil {
		return reminderSchedule{}, err
	}
	next = next.UTC()
	lockedTimezone := ""
	if calendarRule {
		lockedTimezone = cadence.Location.String()
	}
	return reminderSchedule{FireAt: next, Cadence: &cadence, Timezone: lockedTimezone, CadenceSlot: &next}, nil
}

func reminderInitiatorTimezone(ctx context.Context, exec db.DBTX, userID pgtype.UUID) string {
	if !userID.Valid {
		return "UTC"
	}
	var timezone pgtype.Text
	if err := exec.QueryRow(ctx, `SELECT timezone FROM "user" WHERE id = $1`, userID).Scan(&timezone); err != nil || !timezone.Valid || strings.TrimSpace(timezone.String) == "" {
		return "UTC"
	}
	location, err := time.LoadLocation(strings.TrimSpace(timezone.String))
	if err != nil || location.String() == "Local" {
		return "UTC"
	}
	return location.String()
}

var reminderWeekdayByName = map[string]time.Weekday{
	"sun": time.Sunday,
	"mon": time.Monday,
	"tue": time.Tuesday,
	"wed": time.Wednesday,
	"thu": time.Thursday,
	"fri": time.Friday,
	"sat": time.Saturday,
}

var reminderWeekdayName = map[time.Weekday]string{
	time.Sunday:    "sun",
	time.Monday:    "mon",
	time.Tuesday:   "tue",
	time.Wednesday: "wed",
	time.Thursday:  "thu",
	time.Friday:    "fri",
	time.Saturday:  "sat",
}

func parseReminderCadence(raw, timezone string) (reminderCadence, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(raw, "every:") {
		value := strings.TrimPrefix(raw, "every:")
		if len(value) < 2 {
			return reminderCadence{}, fmt.Errorf("repeat must be every:Nm, every:Nh, every:Nd, daily@HH:MM, or weekly:days@HH:MM")
		}
		unit := value[len(value)-1]
		count, err := strconv.ParseInt(value[:len(value)-1], 10, 64)
		if err != nil || count <= 0 {
			return reminderCadence{}, fmt.Errorf("repeat interval must be a positive integer")
		}
		var base time.Duration
		switch unit {
		case 'm':
			base = time.Minute
		case 'h':
			base = time.Hour
		case 'd':
			base = 24 * time.Hour
		default:
			return reminderCadence{}, fmt.Errorf("repeat interval unit must be m, h, or d")
		}
		if count > int64(reminderMaxDelay/base) {
			return reminderCadence{}, fmt.Errorf("repeat interval must not exceed 90 days")
		}
		if strings.TrimSpace(timezone) != "" {
			return reminderCadence{}, fmt.Errorf("timezone is only valid for daily or weekly repeat rules")
		}
		return reminderCadence{
			Canonical: fmt.Sprintf("every:%d%c", count, unit),
			Kind:      reminderCadenceEvery,
			Interval:  time.Duration(count) * base,
			Location:  time.UTC,
		}, nil
	}

	if strings.HasPrefix(raw, "daily@") {
		hour, minute, err := parseReminderWallClock(strings.TrimPrefix(raw, "daily@"))
		if err != nil {
			return reminderCadence{}, err
		}
		location, err := loadReminderLocation(timezone)
		if err != nil {
			return reminderCadence{}, err
		}
		return reminderCadence{
			Canonical: fmt.Sprintf("daily@%02d:%02d", hour, minute),
			Kind:      reminderCadenceDaily,
			Hour:      hour,
			Minute:    minute,
			Location:  location,
		}, nil
	}

	if strings.HasPrefix(raw, "weekly:") {
		daysAndTime := strings.TrimPrefix(raw, "weekly:")
		at := strings.LastIndex(daysAndTime, "@")
		if at <= 0 || at == len(daysAndTime)-1 {
			return reminderCadence{}, fmt.Errorf("weekly repeat must be weekly:days@HH:MM")
		}
		hour, minute, err := parseReminderWallClock(daysAndTime[at+1:])
		if err != nil {
			return reminderCadence{}, err
		}
		seen := map[time.Weekday]struct{}{}
		for _, rawDay := range strings.Split(daysAndTime[:at], ",") {
			day, ok := reminderWeekdayByName[strings.TrimSpace(rawDay)]
			if !ok {
				return reminderCadence{}, fmt.Errorf("weekly repeat days must use mon,tue,wed,thu,fri,sat,sun")
			}
			seen[day] = struct{}{}
		}
		if len(seen) == 0 {
			return reminderCadence{}, fmt.Errorf("weekly repeat requires at least one weekday")
		}
		weekdays := make([]time.Weekday, 0, len(seen))
		for day := range seen {
			weekdays = append(weekdays, day)
		}
		sort.Slice(weekdays, func(i, j int) bool {
			return reminderWeekdaySortKey(weekdays[i]) < reminderWeekdaySortKey(weekdays[j])
		})
		names := make([]string, 0, len(weekdays))
		for _, day := range weekdays {
			names = append(names, reminderWeekdayName[day])
		}
		location, err := loadReminderLocation(timezone)
		if err != nil {
			return reminderCadence{}, err
		}
		return reminderCadence{
			Canonical: fmt.Sprintf("weekly:%s@%02d:%02d", strings.Join(names, ","), hour, minute),
			Kind:      reminderCadenceWeekly,
			Hour:      hour,
			Minute:    minute,
			Weekdays:  weekdays,
			Location:  location,
		}, nil
	}

	return reminderCadence{}, fmt.Errorf("repeat must be every:Nm, every:Nh, every:Nd, daily@HH:MM, or weekly:days@HH:MM")
}

func parseReminderWallClock(raw string) (int, int, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return 0, 0, fmt.Errorf("repeat wall clock must be HH:MM")
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("repeat wall clock must be a valid 24-hour HH:MM")
	}
	return hour, minute, nil
}

func loadReminderLocation(timezone string) (*time.Location, error) {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return nil, fmt.Errorf("daily and weekly repeat rules require an IANA timezone")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil || location.String() == "Local" {
		return nil, fmt.Errorf("invalid IANA timezone")
	}
	return location, nil
}

func reminderWeekdaySortKey(day time.Weekday) int {
	if day == time.Sunday {
		return 7
	}
	return int(day)
}

func nextReminderCadence(cadence reminderCadence, after time.Time) (time.Time, error) {
	after = after.UTC()
	if cadence.Kind == reminderCadenceEvery {
		return after.Add(cadence.Interval).UTC(), nil
	}
	if cadence.Location == nil {
		return time.Time{}, fmt.Errorf("reminder cadence timezone is missing")
	}
	localAfter := after.In(cadence.Location)
	for dayOffset := 0; dayOffset <= 370; dayOffset++ {
		date := localAfter.AddDate(0, 0, dayOffset)
		if cadence.Kind == reminderCadenceWeekly && !reminderWeekdayAllowed(cadence.Weekdays, date.Weekday()) {
			continue
		}
		candidates := reminderWallClockCandidates(cadence.Location, date.Year(), date.Month(), date.Day(), cadence.Hour, cadence.Minute)
		for _, candidate := range candidates {
			if candidate.After(after) {
				return candidate.UTC(), nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("could not compute next reminder occurrence")
}

func nextReminderCadenceAfterSlot(cadence reminderCadence, slot, now time.Time) (time.Time, error) {
	if cadence.Kind == reminderCadenceEvery {
		next := slot.UTC().Add(cadence.Interval)
		if next.After(now) {
			return next, nil
		}
		missed := now.Sub(next)/cadence.Interval + 1
		return next.Add(missed * cadence.Interval), nil
	}
	if cadence.Location == nil {
		return time.Time{}, fmt.Errorf("reminder cadence timezone is missing")
	}
	// A calendar cadence represents one local-date slot. During a fall-back
	// overlap the same wall clock has two real instants; once either candidate
	// has fired, advance beyond that local date rather than firing it twice.
	localSlot := slot.In(cadence.Location)
	nextLocalDate := time.Date(localSlot.Year(), localSlot.Month(), localSlot.Day(), 0, 0, 0, 0, cadence.Location).AddDate(0, 0, 1)
	after := now.UTC()
	endOfSlotDate := nextLocalDate.UTC().Add(-time.Nanosecond)
	if endOfSlotDate.After(after) {
		after = endOfSlotDate
	}
	return nextReminderCadence(cadence, after)
}

func reminderWeekdayAllowed(days []time.Weekday, candidate time.Weekday) bool {
	for _, day := range days {
		if day == candidate {
			return true
		}
	}
	return false
}

// reminderWallClockCandidates returns all instants that render as the requested
// local wall clock. During a fall-back overlap this returns both instants. If
// the wall clock does not exist during a spring-forward gap, it returns the
// first valid minute after the gap on the same local date.
func reminderWallClockCandidates(location *time.Location, year int, month time.Month, day, hour, minute int) []time.Time {
	localMidnight := time.Date(year, month, day, 0, 0, 0, 0, location)
	start := localMidnight.UTC().Add(-4 * time.Hour)
	end := localMidnight.UTC().Add(30 * time.Hour)
	var exact []time.Time
	var firstAfterGap time.Time
	wantMinutes := hour*60 + minute
	for instant := start; !instant.After(end); instant = instant.Add(time.Minute) {
		local := instant.In(location)
		if local.Year() != year || local.Month() != month || local.Day() != day {
			continue
		}
		wallMinutes := local.Hour()*60 + local.Minute()
		if wallMinutes == wantMinutes {
			exact = append(exact, instant)
			continue
		}
		if wallMinutes > wantMinutes && firstAfterGap.IsZero() {
			firstAfterGap = instant
		}
	}
	if len(exact) > 0 {
		sort.Slice(exact, func(i, j int) bool { return exact[i].Before(exact[j]) })
		return exact
	}
	if !firstAfterGap.IsZero() {
		return []time.Time{firstAfterGap}
	}
	return nil
}
