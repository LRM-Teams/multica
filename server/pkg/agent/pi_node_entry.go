package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// piNodeEntryRE extracts the Node .js entrypoint an npm-generated pi launcher
// wraps. npm's cmd-shim, PowerShell, and sh shims all reference the entrypoint
// as "<basedir><sep?><relative-path>.js", where the basedir marker is
// %dp0% / %~dp0 (cmd) or $basedir (PowerShell + sh). The separator is optional
// because %~dp0 already carries a trailing slash. The character class stops at
// a quote, so "%dp0%\node.exe" and the "SET PATHEXT=%PATHEXT:;.JS;=;%" line are
// never mistaken for the entrypoint.
var piNodeEntryRE = regexp.MustCompile(`(?:%~?dp0%?|\$basedir)[\\/]*([^"'\r\n]+?\.[cm]?js)`)

// resolvePiNodeEntry inspects the npm launcher that PATH lookup resolved for pi
// on Windows and returns the Node executable plus the .js entrypoint to run, so
// the daemon can spawn `node <entry> <args>` directly instead of routing the
// prompt through pi.cmd → powershell -File pi.ps1 → "$input | & node".
//
// Rationale: the npm PowerShell shim pipes a redirected prompt via
// `$input | & node`, and Windows PowerShell re-encodes that stdin pipe through
// the console codepage — ASCII on Windows PowerShell 5.1 (non-ASCII bytes
// collapse to '?'), the OEM codepage on 7 (CJK turns to mojibake). Making node
// the first reader of the pipe sidesteps PowerShell entirely: node decodes
// stdin as raw UTF-8, exactly like pi's macOS/Linux shebang binstub. See
// PowerShell/PowerShell#14945 and the daemon's own --content-file guidance
// (execenv/reply_instructions.go) for the same class of bug.
//
// Returns ok=false when the launcher isn't a recognised npm shim, no parsed
// .js entrypoint resolves to a real file, or node can't be located. Callers
// then fall back to the powershell -File path, which still fixes the #3306 argv
// re-tokenisation (it just keeps the encoding bug it was never meant to solve).
//
// statFn / readFile / lookPath are injected so the parser stays unit-testable
// off Windows; production callers pass os.Stat, os.ReadFile, exec.LookPath.
func resolvePiNodeEntry(
	shimPath string,
	statFn func(string) (os.FileInfo, error),
	readFile func(string) ([]byte, error),
	lookPath func(string) (string, error),
) (nodeExe string, entry string, ok bool) {
	ext := strings.ToLower(filepath.Ext(shimPath))
	if ext != ".cmd" && ext != ".bat" && ext != ".ps1" {
		return "", "", false
	}
	dir := filepath.Dir(shimPath)
	base := strings.TrimSuffix(filepath.Base(shimPath), filepath.Ext(shimPath))

	entry = extractPiNodeEntry(dir, base, statFn, readFile)
	if entry == "" {
		return "", "", false
	}
	node, ok := resolvePiNode(dir, statFn, lookPath)
	if !ok {
		return "", "", false
	}
	return node, entry, true
}

// extractPiNodeEntry reads the candidate npm shims for `base` in `dir`
// (base.cmd, base.ps1, and the extensionless sh binstub) and returns the first
// parsed .js entrypoint that resolves to an existing file. Reading siblings —
// not just the launcher PATH resolved — covers thin .cmd wrappers that only
// delegate to the .ps1.
func extractPiNodeEntry(
	dir, base string,
	statFn func(string) (os.FileInfo, error),
	readFile func(string) ([]byte, error),
) string {
	for _, name := range []string{base + ".cmd", base + ".ps1", base} {
		data, err := readFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, m := range piNodeEntryRE.FindAllStringSubmatch(string(data), -1) {
			rel := filepath.FromSlash(strings.ReplaceAll(m[1], `\`, "/"))
			cand := filepath.Join(dir, rel)
			if fileExists(statFn, cand) {
				return cand
			}
		}
	}
	return ""
}

// resolvePiNode picks the Node executable to spawn. A node.exe colocated with
// the shim (nvm-for-windows, npm global prefix) mirrors the shim's own
// "IF EXIST %dp0%\node.exe" preference and avoids a PATH mismatch when several
// node versions are installed; otherwise fall back to PATH.
func resolvePiNode(
	dir string,
	statFn func(string) (os.FileInfo, error),
	lookPath func(string) (string, error),
) (string, bool) {
	if cand := filepath.Join(dir, "node.exe"); fileExists(statFn, cand) {
		return cand, true
	}
	for _, name := range []string{"node.exe", "node"} {
		if p, err := lookPath(name); err == nil && p != "" {
			return p, true
		}
	}
	return "", false
}

func fileExists(statFn func(string) (os.FileInfo, error), path string) bool {
	_, err := statFn(path)
	return err == nil
}
