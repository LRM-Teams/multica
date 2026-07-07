package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fakeReadFile returns a readFile stub backed by an in-memory name→body map.
func fakeReadFile(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		if body, ok := files[path]; ok {
			return []byte(body), nil
		}
		return nil, errors.New("not found")
	}
}

// fakeLookPath returns a lookPath stub: names in `found` resolve to
// <name>-resolved, everything else errors.
func fakeLookPath(found ...string) func(string) (string, error) {
	set := make(map[string]struct{}, len(found))
	for _, n := range found {
		set[n] = struct{}{}
	}
	return func(name string) (string, error) {
		if _, ok := set[name]; ok {
			return name + "-resolved", nil
		}
		return "", errors.New("not found")
	}
}

// realNpmCmdShim mirrors the body npm's cmd-shim writes for a scoped node CLI,
// including the PATHEXT line whose ".JS" token must NOT be mistaken for the
// entrypoint.
func realNpmCmdShim(relJS string) string {
	return "@ECHO off\r\n" +
		"GOTO start\r\n" +
		":find_dp0\r\n" +
		"SET dp0=%~dp0\r\n" +
		"EXIT /b\r\n" +
		":start\r\n" +
		"SETLOCAL\r\n" +
		"CALL :find_dp0\r\n" +
		"IF EXIST \"%dp0%\\node.exe\" (\r\n" +
		"  SET \"_prog=%dp0%\\node.exe\"\r\n" +
		") ELSE (\r\n" +
		"  SET \"_prog=node\"\r\n" +
		"  SET PATHEXT=%PATHEXT:;.JS;=;%\r\n" +
		")\r\n" +
		"endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & \"%_prog%\"  \"%dp0%\\" + relJS + "\" %*\r\n"
}

// realNpmPS1Shim mirrors npm's PowerShell shim body — the `$input | & node`
// pipeline that corrupts non-ASCII stdin and motivates this whole fix.
func realNpmPS1Shim(relJS string) string {
	return "#!/usr/bin/env pwsh\r\n" +
		"$basedir=Split-Path $MyInvocation.MyCommand.Definition -Parent\r\n" +
		"$exe=\"\"\r\n" +
		"if ($PSVersionTable.PSVersion -lt \"6.0\") { $exe=\".exe\" }\r\n" +
		"if (Test-Path \"$basedir/node$exe\") {\r\n" +
		"  if ($MyInvocation.ExpectingInput) {\r\n" +
		"    $input | & \"$basedir/node$exe\"  \"$basedir/" + relJS + "\" $args\r\n" +
		"  } else {\r\n" +
		"    & \"$basedir/node$exe\"  \"$basedir/" + relJS + "\" $args\r\n" +
		"  }\r\n" +
		"}\r\n"
}

func TestResolvePiNodeEntry_RealCmdShimResolvesNodeAndEntry(t *testing.T) {
	dir := `C:\Users\X\AppData\Roaming\npm`
	relJS := `node_modules\@pilabs\cli\bin\pi.js`
	shim := filepath.Join(dir, "pi.cmd")
	entryPath := filepath.Join(dir, filepath.FromSlash("node_modules/@pilabs/cli/bin/pi.js"))

	files := map[string]string{shim: realNpmCmdShim(relJS)}
	statFn := fakeStat(entryPath) // node.exe not colocated → resolved via PATH
	lookPath := fakeLookPath("node.exe")

	node, entry, ok := resolvePiNodeEntry(shim, statFn, fakeReadFile(files), lookPath)
	if !ok {
		t.Fatalf("expected node-direct resolution, got ok=false")
	}
	if entry != entryPath {
		t.Errorf("entry: got %q want %q", entry, entryPath)
	}
	if node != "node.exe-resolved" {
		t.Errorf("node: got %q want %q", node, "node.exe-resolved")
	}
}

func TestResolvePiNodeEntry_PrefersColocatedNodeExe(t *testing.T) {
	dir := `C:\nvm4w\nodejs`
	relJS := `node_modules/@pilabs/cli/bin/pi.js`
	shim := filepath.Join(dir, "pi.ps1")
	entryPath := filepath.Join(dir, filepath.FromSlash(relJS))
	nodeExe := filepath.Join(dir, "node.exe")

	files := map[string]string{shim: realNpmPS1Shim(relJS)}
	statFn := fakeStat(entryPath, nodeExe)
	// lookPath finds nothing; colocated node.exe must win.
	node, entry, ok := resolvePiNodeEntry(shim, statFn, fakeReadFile(files), fakeLookPath())
	if !ok {
		t.Fatalf("expected resolution via colocated node.exe, got ok=false")
	}
	if node != nodeExe {
		t.Errorf("node: got %q want colocated %q", node, nodeExe)
	}
	if entry != entryPath {
		t.Errorf("entry: got %q want %q", entry, entryPath)
	}
}

func TestResolvePiNodeEntry_ReadsPS1SiblingWhenCmdHasNoEntry(t *testing.T) {
	// The resolved launcher (pi.cmd) is a thin wrapper with no .js reference
	// (some third-party shims only delegate); the real entrypoint lives in the
	// sibling pi.ps1. Resolver must fall through to it.
	dir := `C:\tools`
	relJS := `node_modules/pi/dist/cli.js`
	cmd := filepath.Join(dir, "pi.cmd")
	ps1 := filepath.Join(dir, "pi.ps1")
	entryPath := filepath.Join(dir, filepath.FromSlash(relJS))

	files := map[string]string{
		cmd: "@echo off\r\npowershell -NoProfile -File \"%~dp0pi.ps1\" %*\r\n",
		ps1: realNpmPS1Shim(relJS),
	}
	node, entry, ok := resolvePiNodeEntry(cmd, fakeStat(entryPath), fakeReadFile(files), fakeLookPath("node"))
	if !ok {
		t.Fatalf("expected resolution from sibling pi.ps1, got ok=false")
	}
	if entry != entryPath {
		t.Errorf("entry: got %q want %q", entry, entryPath)
	}
	if node != "node-resolved" {
		t.Errorf("node: got %q want %q", node, "node-resolved")
	}
}

func TestResolvePiNodeEntry_FalseWhenEntryFileMissing(t *testing.T) {
	dir := `C:\Users\X\AppData\Roaming\npm`
	relJS := `node_modules\pi\bin\pi.js`
	shim := filepath.Join(dir, "pi.cmd")

	files := map[string]string{shim: realNpmCmdShim(relJS)}
	// statFn reports nothing present → the parsed entrypoint doesn't resolve.
	node, entry, ok := resolvePiNodeEntry(shim, fakeStat(), fakeReadFile(files), fakeLookPath("node.exe"))
	if ok {
		t.Fatalf("expected ok=false when entrypoint file is absent, got node=%q entry=%q", node, entry)
	}
}

func TestResolvePiNodeEntry_FalseWhenNodeUnresolvable(t *testing.T) {
	dir := `C:\Users\X\AppData\Roaming\npm`
	relJS := `node_modules/pi/bin/pi.js`
	shim := filepath.Join(dir, "pi.cmd")
	entryPath := filepath.Join(dir, filepath.FromSlash(relJS))

	files := map[string]string{shim: realNpmCmdShim(relJS)}
	// Entry resolves, but neither a colocated node.exe nor PATH node exists.
	if node, entry, ok := resolvePiNodeEntry(shim, fakeStat(entryPath), fakeReadFile(files), fakeLookPath()); ok {
		t.Fatalf("expected ok=false when node cannot be located, got node=%q entry=%q", node, entry)
	}
}

func TestResolvePiNodeEntry_SkipsNonShimExtensions(t *testing.T) {
	// exec.LookPath returned a real binary (or a shebang binstub) — nothing to
	// parse; resolver must decline so the caller uses its normal path.
	for _, name := range []string{"pi.exe", "pi", "/usr/local/bin/pi"} {
		shim := name
		files := map[string]string{shim: realNpmCmdShim(`node_modules/pi/bin/pi.js`)}
		if _, _, ok := resolvePiNodeEntry(shim, fakeStat("anything"), fakeReadFile(files), fakeLookPath("node")); ok {
			t.Errorf("path %q: expected ok=false for non-.cmd/.ps1 launcher", name)
		}
	}
}

func TestResolvePiNodeEntry_IgnoresPathextJSToken(t *testing.T) {
	// Regression guard: the "SET PATHEXT=%PATHEXT:;.JS;=;%" line contains ".JS".
	// If the resolver latched onto it, entry would be a garbage path that never
	// stat-resolves. Prove the real entrypoint is the one selected.
	dir := `C:\npm`
	relJS := `node_modules/pi/bin/pi.js`
	shim := filepath.Join(dir, "pi.cmd")
	entryPath := filepath.Join(dir, filepath.FromSlash(relJS))
	bogus := filepath.Join(dir, "PATHEXT") // must never be chosen

	files := map[string]string{shim: realNpmCmdShim(relJS)}
	// Both the real entry and a decoy exist; correct parse picks the .js one.
	statFn := func(path string) (os.FileInfo, error) {
		if path == entryPath || path == bogus {
			return nil, nil
		}
		return nil, errors.New("not found")
	}
	_, entry, ok := resolvePiNodeEntry(shim, statFn, fakeReadFile(files), fakeLookPath("node.exe"))
	if !ok {
		t.Fatalf("expected resolution, got ok=false")
	}
	if entry != entryPath {
		t.Errorf("entry: got %q want %q (PATHEXT .JS token must be ignored)", entry, entryPath)
	}
}
