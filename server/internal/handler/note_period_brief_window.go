package handler

import (
	"fmt"
	"strings"
	"time"
)

const (
	noteRetrospectiveWindowCustom noteRetrospectiveWindowKind = "custom"
	// Cap custom Period Brief windows so Facts queries stay bounded.
	notePeriodBriefCustomMaxDays = 93
)

// resolveNotePeriodBriefWindow resolves day|week|month via the shared
// retrospective calendar rules, or a custom inclusive [start_date, end_date]
// calendar range in the viewing timezone (stored as half-open UTC).
func resolveNotePeriodBriefWindow(
	kind noteRetrospectiveWindowKind,
	dateYYYYMMDD, startDate, endDate, tz string,
	now time.Time,
) (noteRetrospectiveWindow, error) {
	if kind == noteRetrospectiveWindowCustom {
		return resolveNotePeriodBriefCustomWindow(startDate, endDate, tz)
	}
	return resolveNoteRetrospectiveWindow(kind, dateYYYYMMDD, tz, now)
}

func resolveNotePeriodBriefCustomWindow(startDate, endDate, tz string) (noteRetrospectiveWindow, error) {
	loc := resolveNoteRetrospectiveLocation(tz)
	tzName := loc.String()
	if tzName == "Local" {
		tzName = "UTC"
		loc = time.UTC
	}
	startRaw := strings.TrimSpace(startDate)
	endRaw := strings.TrimSpace(endDate)
	if startRaw == "" || endRaw == "" {
		return noteRetrospectiveWindow{}, fmt.Errorf("custom window requires start_date and end_date (YYYY-MM-DD)")
	}
	startLocal, err := time.ParseInLocation("2006-01-02", startRaw, loc)
	if err != nil {
		return noteRetrospectiveWindow{}, fmt.Errorf("invalid start_date %q (want YYYY-MM-DD)", startRaw)
	}
	endDayLocal, err := time.ParseInLocation("2006-01-02", endRaw, loc)
	if err != nil {
		return noteRetrospectiveWindow{}, fmt.Errorf("invalid end_date %q (want YYYY-MM-DD)", endRaw)
	}
	if endDayLocal.Before(startLocal) {
		return noteRetrospectiveWindow{}, fmt.Errorf("end_date must be on or after start_date")
	}
	// Inclusive end calendar day → exclusive next midnight.
	endLocal := endDayLocal.AddDate(0, 0, 1)
	days := int(endDayLocal.Sub(startLocal).Hours()/24) + 1
	if days > notePeriodBriefCustomMaxDays {
		return noteRetrospectiveWindow{}, fmt.Errorf(
			"custom window too long (%d days; max %d)", days, notePeriodBriefCustomMaxDays,
		)
	}
	return noteRetrospectiveWindow{
		Kind:     noteRetrospectiveWindowCustom,
		Timezone: tzName,
		Start:    startLocal.UTC(),
		End:      endLocal.UTC(),
		Label:    fmt.Sprintf("%s→%s", startLocal.Format("2006-01-02"), endDayLocal.Format("2006-01-02")),
	}, nil
}
