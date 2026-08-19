//go:build !windows

package computer

// windowsMachineGuid is only meaningful on Windows; the caller never reaches
// it on other platforms (MachineID switches on runtime.GOOS).
func windowsMachineGuid() string { return "" }
