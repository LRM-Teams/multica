package computer

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// machineIDFileCandidates are the Linux locations that hold the OS-level
// machine fingerprint. /etc/machine-id is the systemd canonical one;
// /var/lib/dbus/machine-id is the historical fallback on systems without
// systemd (or before systemd adopted the file).
var machineIDFileCandidates = []string{
	"/etc/machine-id",
	"/var/lib/dbus/machine-id",
}

// MachineID returns the OS-level persistent machine fingerprint:
//   - Linux:   /etc/machine-id (or the dbus fallback)
//   - macOS:   IOPlatformUUID from ioreg (IOPlatformExpertDevice)
//   - Windows: MachineGuid under HKLM\SOFTWARE\Microsoft\Cryptography
//
// It is an attribute of the physical machine, independent of ~/.multica,
// and survives identity rebuilds (LRM-1570). It is best-effort: any
// failure returns "" so callers fall back to hostname-based matching.
func MachineID() string {
	switch runtime.GOOS {
	case "linux":
		for _, path := range machineIDFileCandidates {
			if id := readMachineIDFile(path); id != "" {
				return id
			}
		}
	case "darwin":
		if id := macIOPlatformUUID(); id != "" {
			return id
		}
	case "windows":
		if id := windowsMachineGuid(); id != "" {
			return id
		}
	}
	return ""
}

func readMachineIDFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if id == "" || strings.ContainsAny(id, " \t\n\r") {
		return ""
	}
	return id
}

// macIOPlatformUUID reads the hardware UUID via ioreg. It is the same value
// Apple shows under "Hardware UUID" in System Information.
func macIOPlatformUUID() string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "IOPlatformUUID") {
			if idx := strings.Index(line, "="); idx >= 0 {
				id := strings.TrimSpace(strings.Trim(line[idx+1:], `"`))
				if id != "" {
					return id
				}
			}
		}
	}
	return ""
}
