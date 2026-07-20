package agent

const windowsProcessStillActive = 259

type windowsProcessOpenResult uint8

const (
	windowsProcessOpenSucceeded windowsProcessOpenResult = iota
	windowsProcessOpenAccessDenied
	windowsProcessOpenNotFound
	windowsProcessOpenUnknown
)

// windowsProcessLivenessDecision is platform-independent so every Windows
// error path is executable in the normal test suite, including on non-Windows
// CI hosts. The Windows adapter only maps API results into these facts.
func windowsProcessLivenessDecision(openResult windowsProcessOpenResult, exitCode uint32, exitCodeKnown bool) (alive bool, known bool) {
	switch openResult {
	case windowsProcessOpenAccessDenied:
		// Access denied proves the process exists, matching Unix EPERM.
		return true, true
	case windowsProcessOpenNotFound:
		return false, true
	case windowsProcessOpenUnknown:
		return false, false
	case windowsProcessOpenSucceeded:
		if !exitCodeKnown {
			return false, false
		}
		return exitCode == windowsProcessStillActive, true
	default:
		return false, false
	}
}
