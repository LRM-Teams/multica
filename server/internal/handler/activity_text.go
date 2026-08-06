package handler

import "github.com/jackc/pgx/v5/pgtype"

// truncateForActivity bounds server-owned presentation text before it reaches
// an Activity entry or another user-visible, bounded context.
func truncateForActivity(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func stringPtr(value string) *string {
	return &value
}

func nullableUUIDArg(value pgtype.UUID) any {
	if !value.Valid {
		return nil
	}
	return value
}
