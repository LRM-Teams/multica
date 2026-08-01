package handler

import "strings"

// deviceInfoSeparator is the glue daemon registration uses when composing
// DeviceInfo from req.DeviceName and runtime.Version (see daemon.go).
const deviceInfoSeparator = " · "

// deviceNameFromRuntime returns the machine/OS label for the Basics → OS row.
// Prefer the structured metadata.device_name persisted at registration (daemon
// already sends device_name separately). Only when that is missing — older
// rows written before we stored it — invert our own glue:
//
//	device_info = device_name · version   → left half
//	device_info = device_name             → whole string
//	device_info = version                 → empty (matches metadata.version)
//
// No version-shape heuristics: we do not guess whether a half "looks like" a CA.
func deviceNameFromRuntime(deviceInfo string, metadata any) string {
	if name := metadataString(metadata, "device_name"); name != "" {
		return name
	}
	return deviceNameFromLegacyDeviceInfo(deviceInfo, metadataString(metadata, "version"))
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

func deviceNameFromLegacyDeviceInfo(deviceInfo, agentVersion string) string {
	deviceInfo = strings.TrimSpace(deviceInfo)
	if deviceInfo == "" {
		return ""
	}
	if name, version, found := strings.Cut(deviceInfo, deviceInfoSeparator); found {
		_ = version
		return strings.TrimSpace(name)
	}
	// Registration wrote version alone when DeviceName was empty.
	if agentVersion != "" && deviceInfo == strings.TrimSpace(agentVersion) {
		return ""
	}
	return deviceInfo
}
