//go:build windows

package agent

import (
	"log/slog"
	"os"
	"os/exec"
)

// platformPiInvocation selects how to spawn pi on Windows.
//
// Preferred path: spawn node directly on pi's JS entrypoint. The daemon pipes
// the (often CJK) chat prompt to the child's stdin, and the npm pi.cmd →
// powershell -File pi.ps1 → "$input | & node" chain re-encodes that pipe
// through the console codepage, corrupting non-ASCII bytes ('?' on Windows
// PowerShell 5.1, mojibake on 7). Running node as the direct child makes node
// the first reader of the pipe, so it decodes stdin as raw UTF-8 — matching
// pi's macOS/Linux shebang binstub. See resolvePiNodeEntry.
//
// Fallback: when the npm launcher can't be parsed into a node+entrypoint pair
// (third-party wrapper, missing files, node not on PATH), rewrite pi.cmd →
// powershell -File pi.ps1 to at least preserve argv tokens (#3306).
// powerShellLookup and rewriteCmdToPS1 are defined in cursor_invocation_windows.go.
func platformPiInvocation(lookedUp string, args []string, logger *slog.Logger) (string, []string, bool) {
	if node, entry, ok := resolvePiNodeEntry(lookedUp, os.Stat, os.ReadFile, exec.LookPath); ok {
		full := make([]string, 0, 1+len(args))
		full = append(full, entry)
		full = append(full, args...)
		if logger != nil {
			logger.Info("pi: launching node directly to preserve UTF-8 stdin bytes",
				"node", node,
				"entry", entry,
				"shim", lookedUp,
			)
		}
		return node, full, true
	}
	return rewriteCmdToPS1("pi", lookedUp, args, logger)
}
