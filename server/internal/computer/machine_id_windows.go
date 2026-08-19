//go:build windows

package computer

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// windowsMachineGuid reads the per-installation MachineGuid registry value,
// the canonical Windows machine fingerprint.
func windowsMachineGuid() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	guid, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(guid)
}
