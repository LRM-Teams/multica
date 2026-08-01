package handler

import "strings"

// deviceInfoSeparatorPrefix is the glue daemon registration uses when composing
// DeviceInfo from OS/device name and runtime version (see daemon.go:
// fmt.Sprintf("%s · %s", deviceName, version)). Match on " ·" so a trailing
// separator after TrimSpace still splits correctly.
const deviceInfoSeparatorPrefix = " ·"

// splitDeviceInfo separates a legacy composite device_info string into a
// device/OS name and a runtime version. Older daemons keep sending the glued
// form; the API exposes both pieces so clients don't have to re-parse.
//
// Rules:
//   - "ubuntu · codex-cli 0.146.0" → ("ubuntu", "codex-cli 0.146.0")
//   - no separator → whole string is the device name; runtime version empty
//   - empty input → both empty
func splitDeviceInfo(raw string) (deviceName, runtimeVersion string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	idx := strings.Index(raw, deviceInfoSeparatorPrefix)
	if idx < 0 {
		return raw, ""
	}
	name := strings.TrimSpace(raw[:idx])
	version := strings.TrimSpace(raw[idx+len(deviceInfoSeparatorPrefix):])
	return name, version
}
