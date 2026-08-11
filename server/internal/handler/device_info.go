package handler

import "strings"

// deviceNameFromRuntime returns the machine label from registration metadata.
// Only the structured metadata.device_name persisted at registration counts —
// daemon already sends device_name separately. Rows that predate that persist
// return empty until the daemon re-registers (no device_info parsing).
func deviceNameFromRuntime(_ string, metadata any) string {
	return metadataString(metadata, "device_name")
}

// operatingSystemFromRuntime returns the daemon-reported GOOS value. It is
// deliberately separate from device_name, whose default is the hostname.
func operatingSystemFromRuntime(metadata any) string {
	return metadataString(metadata, "os")
}

func metadataString(metadata any, key string) string {
	m, ok := metadata.(map[string]any)
	if !ok {
		return ""
	}
	value, ok := m[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}
