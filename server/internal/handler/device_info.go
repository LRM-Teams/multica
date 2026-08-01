package handler

import (
	"regexp"
	"strings"
)

// deviceInfoSeparator is the glue daemon registration uses when composing
// DeviceInfo (see daemon.go: fmt.Sprintf("%s · %s", deviceName, version)).
const deviceInfoSeparator = " · "

// isAgentVersionLike matches parts that carry an agent CLI version, not
// machine/OS info — e.g. "2.1.5 (Claude Code)", "codex-cli 0.118.0",
// "claude 1.0.0". Same filter the FE machine subtitle / machineOsLabel used.
var isAgentVersionLike = regexp.MustCompile(`(?:^|\s)v?\d+\.\d+\.\d+`)

// prettyOSArch matches reshaped OS+arch halves such as "Linux (x86_64)".
var prettyOSArch = regexp.MustCompile(`(?i)^(macOS|Linux|Windows|FreeBSD|OpenBSD|NetBSD)\s+\([^)]+\)$`)

var daemonPlaceholder = regexp.MustCompile(`(?i)^daemon\b`)

// deviceNameFromDeviceInfo derives the Basics → OS label from a legacy
// composite device_info string. CA version halves are dropped; when a
// pretty OS-arch half is present it wins over a hostname half.
//
// Examples (Iris / #1722 fixtures):
//   - "ubuntu · codex-cli 0.146.0" → "ubuntu"
//   - "dev.local · 2.1.5 (Claude Code)" → "dev.local"
//   - "host.local · Linux (x86_64)" → "Linux (x86_64)"
//   - "daemon abc123" → ""
func deviceNameFromDeviceInfo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || daemonPlaceholder.MatchString(raw) {
		return ""
	}

	parts := strings.Split(raw, deviceInfoSeparator)
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || isAgentVersionLike.MatchString(part) {
			continue
		}
		kept = append(kept, part)
	}
	if len(kept) == 0 {
		return ""
	}
	for _, part := range kept {
		if prettyOSArch.MatchString(part) {
			return part
		}
	}
	return strings.Join(kept, deviceInfoSeparator)
}
