# Multica CLI — Installation Guide for AI Agents

> **Audience:** This document is designed for AI agents (Claude Code, Codex, etc.) to read and execute step-by-step. Each step includes the exact shell commands to run, expected outcomes, and error handling.

## One-Line Instruction for Users

Give this instruction to your AI agent:

```
Fetch https://cdn.leagent.me/computer/CLI_INSTALL.md and follow the instructions to install Multica CLI, log in, and start the daemon on this machine.
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
curl -fsSL https://cdn.leagent.me/computer/latest.json -o /tmp/latest.json
LATEST=$(jq -r '.tag' /tmp/latest.json)
URL=$(jq -r --arg p "$PLATFORM" '.platforms[$p].url' /tmp/latest.json)
SHA256=$(jq -r --arg p "$PLATFORM" '.platforms[$p].sha256' /tmp/latest.json)

# Download, verify, and extract
curl -sL "$URL" -o /tmp/multica.tar.gz
ACTUAL_SHA256=$(shasum -a 256 /tmp/multica.tar.gz 2>/dev/null | awk '{print $1}' || sha256sum /tmp/multica.tar.gz | awk '{print $1}')
if [ "$ACTUAL_SHA256" != "$SHA256" ]; then
  echo "Checksum mismatch: expected $SHA256, got $ACTUAL_SHA256" >&2
  exit 1
fi
tar -xzf /tmp/multica.tar.gz -C /tmp multica
mkdir -p "$HOME/.local/bin"
mv /tmp/multica "$HOME/.local/bin/multica"
chmod +x "$HOME/.local/bin/multica"
rm /tmp/multica.tar.gz /tmp/latest.json
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
5. A daemon that was already started from the old path keeps that executable.
   Wait until no agent task is running on the affected profile, then restart it
   explicitly through the canonical binary:

```bash
"$HOME/.local/bin/multica" daemon status
"$HOME/.local/bin/multica" daemon restart
"$HOME/.local/bin/multica" daemon status
"$HOME/.local/bin/multica" version
```

For every named profile that was started from the old installation, repeat the
same adoption gate with the profile before `daemon`:

```bash
"$HOME/.local/bin/multica" --profile staging daemon restart
"$HOME/.local/bin/multica" --profile staging daemon status
```

Do not restart while an agent task is active. Adoption is complete when
`daemon status` reports the same `Version` as the canonical `multica version`.

### Option C: Windows (PowerShell)

Run in PowerShell (no admin required):

```powershell
irm https://cdn.leagent.me/computer/install.ps1 | iex
```

This downloads the latest Windows binary from the release feed, installs it to `%USERPROFILE%\.multica\bin\`, and adds it to your user PATH.

Verify:

```powershell
multica version
```

**If this fails:**
- Restart your terminal so the updated PATH takes effect.
- If your execution policy blocks the script: `Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned` then re-run.

---

## Step 3: Log in

Run:

```bash
multica login
```

**Important:** This command opens a browser window for OAuth authentication. Tell the user:

> "A browser window will open for Multica login. Please complete the authentication in your browser, then come back here."

Wait for the command to complete. It will automatically discover and watch all workspaces the user belongs to.

Verify:

```bash
multica auth status
```

Expected output should show the authenticated user and server URL.

**If login fails:**
- If no browser is available (headless environment), the user can generate a Personal Access Token at `https://app.multica.ai/settings` and run: `multica login --token <mul_...>` (use `--token=` with an empty value to be prompted interactively).
- If the server URL needs to be customized: `multica config set server_url <url>` before logging in.

---

## Step 4: Start the daemon

First, check if the daemon is already running:

```bash
multica daemon status
```

- **If status is "running" and this was not a migration from another install
  path**: skip to **Step 5**.
- **If status is "running" from an older install path**: do not silently skip
  it. Finish active work, then follow the canonical daemon adoption gate in
  Step 2 before continuing.
- **If status is "stopped"**: start it:

```bash
multica daemon start
```

Wait 3 seconds, then verify:

```bash
multica daemon status
```

Expected output should show `running` status with detected agents (e.g. `claude`, `codex`, `copilot`, `opencode`, `openclaw`, `hermes`, `gemini`, `pi`, `cursor-agent`, `grok`).

**If daemon fails to start:**
- Check logs: `multica daemon logs`
- If a port conflict occurs, the daemon may already be running under a different profile.
- If no agents are detected, ensure at least one AI CLI (`claude`, `codex`, `copilot`, `opencode`, `openclaw`, `hermes`, `gemini`, `pi`, `cursor-agent`, or `grok`) is installed and on the `$PATH`.

---

## Step 5: Verify everything is working

Run:

```bash
multica daemon status
```

Confirm:
1. Status is `running`
2. At least one agent is listed (e.g. `claude`, `codex`, `copilot`, `opencode`, `openclaw`, `hermes`, `gemini`, `pi`, `cursor-agent`, or `grok`)
3. At least one workspace is being watched

If the agents list is empty, tell the user:

> "The Multica daemon is running but no AI agent CLIs were detected. Please install at least one supported CLI (`claude`, `codex`, `copilot`, `opencode`, `openclaw`, `hermes`, `gemini`, `pi`, `cursor-agent`, or `grok`), then restart the daemon with `multica daemon stop && multica daemon start`."

---

## Summary

When all steps are complete, inform the user:

> "Multica CLI is installed and the daemon is running. Agents in your workspaces can now execute tasks on this machine. You can manage workspaces with `multica workspace list` and view daemon logs with `multica daemon logs -f`."
