# Multica CLI — Installation Guide for AI Agents

> **Audience:** This document is designed for AI agents (Claude Code, Codex, etc.) to read and execute step-by-step. Each step includes the exact shell commands to run, expected outcomes, and error handling.

## One-Line Instruction for Users

Give this instruction to your AI agent:

```
Fetch https://cdn.leagent.me/computer/CLI_INSTALL.md and follow the instructions to install Multica CLI, connect one Workspace, and start the Computer on this machine.
```

---

## Step 1: Check if Multica CLI is already installed

Run:

```bash
CLI_PATH=$(command -v multica 2>/dev/null || true)
printf 'Multica path: %s\n' "${CLI_PATH:-not installed}"
[ -n "$CLI_PATH" ] && multica version
```

- **If it prints a version string and the path is `$HOME/.local/bin/multica`**: skip to **Step 3**.
- **If it prints a version from any other path**: continue to **Step 2**. The installer will migrate the CLI to the canonical user-owned path even when the version is already current.
- **If command not found**: continue to **Step 2**.

---

## Step 2: Install the Multica CLI

> **Windows users:** Skip to [Option C: Windows (PowerShell)](#option-c-windows-powershell) below.

### Option A: Install Script (macOS/Linux)

Run:

```bash
curl -fsSL https://cdn.leagent.me/computer/install.sh | bash
```

The default version selector is `latest`. Use the same `--version` option for
the current prerelease or an exact immutable release:

```bash
curl -fsSL https://cdn.leagent.me/computer/install.sh | bash -s -- --version alpha
curl -fsSL https://cdn.leagent.me/computer/install.sh | bash -s -- --version vX.Y.Z-alpha.N
```

`alpha` follows the movable prerelease manifest. An exact tag reads that
version's immutable manifest and fails if the manifest tag differs. Automation
may set `MULTICA_VERSION` to the same values instead of passing the option.
The default `latest` selector reads the canonical root `manifest.json`;
`latest.json` exists only for older clients during migration.

The installer activates the verified binary through the same VersionStore as
`multica computer upgrade`: the public launcher path stays stable, immutable
Active/Previous versions are retained, and a failed activation does not become
the running Computer version.

Then verify:

```bash
multica version
```

If the version prints successfully, skip to **Step 3**.

### Option B: Download directly from the release feed (macOS/Linux)

If the install script is not suitable for your environment, download the binary directly. This reads the same manifest `scripts/install.sh` uses — not GitHub Releases: an unauthenticated request to the private LRM-Teams/multica repo's GitHub API/asset host always 404s, so there is no working GitHub fallback here.

Requires `jq` or `python3` to parse the manifest.

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')   # "darwin" or "linux"
ARCH=$(uname -m)                                # "x86_64" or "arm64"

# Normalize architecture name
if [ "$ARCH" = "x86_64" ]; then
  ARCH="amd64"
fi
PLATFORM="${OS}-${ARCH}"

# Fetch the release manifest
curl -fsSL https://cdn.leagent.me/computer/manifest.json -o /tmp/multica-manifest.json
LATEST=$(jq -r '.tag' /tmp/multica-manifest.json)
URL=$(jq -r --arg p "$PLATFORM" '.platforms[$p].url' /tmp/multica-manifest.json)
SHA256=$(jq -r --arg p "$PLATFORM" '.platforms[$p].sha256' /tmp/multica-manifest.json)

# Download, verify, and extract
curl -sL "$URL" -o /tmp/multica.tar.gz
ACTUAL_SHA256=$(shasum -a 256 /tmp/multica.tar.gz 2>/dev/null | awk '{print $1}' || sha256sum /tmp/multica.tar.gz | awk '{print $1}')
if [ "$ACTUAL_SHA256" != "$SHA256" ]; then
  echo "Checksum mismatch: expected $SHA256, got $ACTUAL_SHA256" >&2
  exit 1
fi
tar -xzf /tmp/multica.tar.gz -C /tmp multica
mkdir -p "$HOME/.local/bin"
chmod +x /tmp/multica
BINARY_SHA256=$(shasum -a 256 /tmp/multica 2>/dev/null | awk '{print $1}' || sha256sum /tmp/multica | awk '{print $1}')
/tmp/multica installer-activate \
  --version "$LATEST" \
  --sha256 "$BINARY_SHA256" \
  --launcher "$HOME/.local/bin/multica"
rm /tmp/multica /tmp/multica.tar.gz /tmp/multica-manifest.json
```

Make the user-owned directory available in the current shell:

```bash
export PATH="$HOME/.local/bin:$PATH"
hash -r 2>/dev/null || true
```

Persist it in the user's shell configuration:

```bash
case "$(basename "${SHELL:-}")" in
  fish)
    mkdir -p "$HOME/.config/fish"
    printf '\n%s\n' 'fish_add_path --prepend --global --move "$HOME/.local/bin"' >>"$HOME/.config/fish/config.fish"
    ;;
  zsh)
    printf '\n%s\n' 'export PATH="$HOME/.local/bin:$PATH"' >>"$HOME/.zshrc"
    ;;
  bash)
    printf '\n%s\n' 'export PATH="$HOME/.local/bin:$PATH"' >>"$HOME/.bashrc"
    BASH_LOGIN_RC="$HOME/.bash_profile"
    [ -f "$HOME/.bash_profile" ] || [ ! -f "$HOME/.bash_login" ] || BASH_LOGIN_RC="$HOME/.bash_login"
    [ -f "$HOME/.bash_profile" ] || [ -f "$HOME/.bash_login" ] || [ ! -f "$HOME/.profile" ] || BASH_LOGIN_RC="$HOME/.profile"
    printf '\n%s\n' 'export PATH="$HOME/.local/bin:$PATH"' >>"$BASH_LOGIN_RC"
    ;;
  *)
    printf '\n%s\n' 'export PATH="$HOME/.local/bin:$PATH"' >>"$HOME/.profile"
    ;;
esac
```

Keep the canonical line as the final PATH prepend in each file. Remove older
duplicate Multica PATH lines or a later legacy `/usr/local/bin`/Homebrew
prepend that shadows it. The install script performs this managed-block
cleanup automatically and is preferred for repeatable installs.

Verify:

```bash
multica version
command -v multica
```

**If this fails:**
- Confirm that `command -v multica` prints `$HOME/.local/bin/multica`.
- Restart the terminal so the updated user PATH takes effect.

### Migrating an older system-owned installation

The macOS/Linux installer has one supported target: `$HOME/.local/bin/multica`. It never writes to a system directory and never elevates privileges.

If `command -v multica` still reports an older system path after installation:

1. Start a fresh shell and run `type -a multica`.
2. Confirm `$HOME/.local/bin/multica version` succeeds.
3. Remove the old system binary through the machine owner or administrator's normal software-management process. The Multica installer deliberately does not modify it.
4. Run `hash -r` (or restart the shell) and confirm `command -v multica` resolves to `$HOME/.local/bin/multica`.
5. A Computer resident that was already started from the old path keeps that executable.
   Wait until no Agent task is running, then restart it
	explicitly through the canonical binary:

```bash
"$HOME/.local/bin/multica" computer status
"$HOME/.local/bin/multica" computer restart
"$HOME/.local/bin/multica" computer status
"$HOME/.local/bin/multica" version
```

Profiles cannot select a second resident. Do not restart while an Agent task is
active. Adoption is complete when `computer status` reports the same `Version`
as the canonical `multica version`.

### Option C: Windows (PowerShell)

Run in PowerShell (no admin required):

```powershell
irm https://cdn.leagent.me/computer/install.ps1 | iex
```

This downloads the latest Windows binary from the release feed, verifies and
activates it through VersionStore at `%USERPROFILE%\.multica\bin\`, and adds
that stable launcher to your user PATH.

Use the same `-Version` parameter for the current prerelease or an exact
immutable release:

```powershell
& ([scriptblock]::Create((irm https://cdn.leagent.me/computer/install.ps1))) -Version alpha
& ([scriptblock]::Create((irm https://cdn.leagent.me/computer/install.ps1))) -Version vX.Y.Z-alpha.N
```

Automation may set `$env:MULTICA_VERSION` to the same values.

Verify:

```powershell
multica version
```

**If this fails:**
- Restart your terminal so the updated PATH takes effect.
- If your execution policy blocks the script: `Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned` then re-run.

---

## Step 3: Connect one Workspace

Run:

```bash
multica setup /<workspace-slug>
```

**Important:** This command opens a browser window for OAuth authentication. Tell the user:

> "A browser window will open for Multica login. Please complete the authentication in your browser, then come back here."

Wait for the command to complete. It connects exactly the requested Workspace
and starts the one machine-wide resident detached.

Verify:

```bash
multica auth status
```

Expected output should show the authenticated user and server URL.

**If login fails:**
- On a headless machine, open the printed verification URL on another device
  and enter the printed device code.
- Production is fixed to the leagent.me service. For an operator-controlled
  test service, rerun setup with
  `--environment test --test-url <http(s)-origin>`; arbitrary custom production
  origins are not supported.

---

## Step 4: Verify the Computer resident

Setup already starts the one detached resident. Check its current truth:

```bash
multica computer status
```

- **If status is "running" and this was not a migration from another install
  path**: skip to **Step 5**.
- **If status is "running" from an older install path**: do not silently skip
  it. Finish active work, then follow the canonical Computer adoption gate in
  Step 2 before continuing.
- **If status is "stopped"**: start it:

```bash
multica computer start
```

Wait 3 seconds, then verify:

```bash
multica computer status
```

Expected output shows the stable Computer Identity, selected environment and
its fixed package source (`stable` for production, `preview` for test), running resident version, and the configured Workspace
connection. Zero detected AI tools does not make Computer setup fail.

**If the Computer fails to start:**
- Check logs: `multica computer logs`.
- Run `multica computer doctor` for process, environment, package source, connection,
  and migration evidence.
- A singleton error means the one machine-wide resident is already owned; it
  never means a second profile should be created.

---

## Step 5: Verify everything is working

Run:

```bash
multica computer status
```

Confirm:
1. Status is `running`
2. The configured and resident environment/package source agree
3. The requested Workspace connection is accepted

If no AI coding tool is detected, Computer setup is still complete. Tell the
user separately:

> "The Multica Computer is connected, but no supported AI coding tool was detected. Install one supported CLI, then run `multica computer restart`."

---

## Summary

When all steps are complete, inform the user:

> "Multica CLI is installed and the Computer is connected to the requested Workspace. You can inspect it with `multica computer status` and follow service logs with `multica computer logs -f`."
