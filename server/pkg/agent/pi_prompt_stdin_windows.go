//go:build windows

package agent

// piPromptViaStdin avoids the Windows/CreateProcess command-line length limit.
// Pi reads piped stdin as the initial non-interactive prompt, so keep the huge
// chat prompt off the PowerShell argv used to launch pi.ps1.
func piPromptViaStdin() bool { return true }
