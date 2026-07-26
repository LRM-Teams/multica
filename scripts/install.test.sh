#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

_setup_sandbox() {
  local tmp="$1"
  local stub_bin="$tmp/stub-bin"
  local payload_dir="$tmp/payload"
  mkdir -p "$stub_bin" "$payload_dir" "$tmp/home"

  cat >"$payload_dir/multica" <<'STUB'
#!/usr/bin/env bash
echo "multica v0.3.2 (commit: test)"
STUB
  chmod +x "$payload_dir/multica"
  tar -czf "$tmp/multica.tar.gz" -C "$payload_dir" multica

  cat >"$stub_bin/curl" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$MULTICA_TEST_CURL_LOG"
if [[ "$*" == *"/multica-ai/"* ]]; then
  echo "stub curl saw old upstream URL: $*" >&2
  exit 31
fi
if [[ "$*" == *"-sI"* ]]; then
  printf 'HTTP/2 302\r\nlocation: https://github.com/LRM-Teams/multica/releases/tag/v0.3.2\r\n'
  exit 0
fi

out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [[ -z "$out" ]]; then
  echo "stub curl expected -o" >&2
  exit 2
fi
cp "$MULTICA_TEST_ARCHIVE" "$out"
STUB
  chmod +x "$stub_bin/curl"

  cat >"$stub_bin/sudo" <<'STUB'
#!/usr/bin/env bash
echo "sudo must never be called by the Multica installer" >&2
exit 99
STUB
  chmod +x "$stub_bin/sudo"
}

_run_installer() {
  local tmp="$1"
  local shell_path="$2"
  local extra_path="${3:-}"
  local out="$tmp/install.out"
  local err="$tmp/install.err"
  local path="$tmp/stub-bin:/usr/bin:/bin"
  if [[ -n "$extra_path" ]]; then
    path="$extra_path:$path"
  fi

  if ! HOME="$tmp/home" \
    SHELL="$shell_path" \
    PATH="$path" \
    MULTICA_BIN_DIR="$tmp/must-not-be-used" \
    MULTICA_TEST_ARCHIVE="$tmp/multica.tar.gz" \
    MULTICA_TEST_CURL_LOG="$tmp/curl.log" \
    bash "$ROOT_DIR/scripts/install.sh" >"$out" 2>"$err"; then
    echo "install.sh exited non-zero" >&2
    cat "$out" >&2 || true
    cat "$err" >&2 || true
    return 1
  fi

  if [[ ! -x "$tmp/home/.local/bin/multica" ]]; then
    echo "expected canonical binary at $tmp/home/.local/bin/multica" >&2
    cat "$out" >&2 || true
    cat "$err" >&2 || true
    return 1
  fi
  if [[ -e "$tmp/must-not-be-used/multica" ]]; then
    echo "MULTICA_BIN_DIR must not create a second install root" >&2
    return 1
  fi
  if ! grep -q "https://github.com/LRM-Teams/multica/releases/download/v0.3.2/multica-cli-0.3.2-" "$tmp/curl.log"; then
    echo "expected download from LRM-Teams release URL" >&2
    cat "$tmp/curl.log" >&2 || true
    return 1
  fi
}

test_default_installs_only_to_user_local_without_sudo() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  _run_installer "$tmp" /bin/zsh

  if [[ ! -f "$tmp/home/.zshrc" ]]; then
    echo "expected zsh PATH config" >&2
    return 1
  fi
  if [[ "$(grep -cF 'export PATH="$HOME/.local/bin:$PATH"' "$tmp/home/.zshrc")" -ne 1 ]]; then
    echo "expected one canonical PATH entry in .zshrc" >&2
    cat "$tmp/home/.zshrc" >&2
    return 1
  fi
}

test_same_version_legacy_path_is_migrated_to_user_local() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  mkdir -p "$tmp/legacy-bin"
  cp "$tmp/payload/multica" "$tmp/legacy-bin/multica"

  _run_installer "$tmp" /bin/bash "$tmp/legacy-bin"

  if ! grep -q "Migrating Multica CLI from $tmp/legacy-bin/multica to the user-owned $tmp/home/.local/bin/multica" "$tmp/install.err"; then
    echo "expected explicit legacy-path migration warning" >&2
    cat "$tmp/install.err" >&2
    return 1
  fi
  if [[ ! -x "$tmp/legacy-bin/multica" ]]; then
    echo "installer must not destructively remove the legacy binary" >&2
    return 1
  fi
  if ! grep -qF "\"$tmp/home/.local/bin/multica\" daemon restart" "$tmp/install.err"; then
    echo "expected explicit idle daemon adoption command" >&2
    cat "$tmp/install.err" >&2
    return 1
  fi
  if ! grep -qF "\"$tmp/home/.local/bin/multica\" --profile staging daemon restart" "$tmp/install.err"; then
    echo "expected named-profile daemon adoption command" >&2
    cat "$tmp/install.err" >&2
    return 1
  fi
}

test_fish_path_config_is_idempotent() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  _run_installer "$tmp" /usr/bin/fish
  _run_installer "$tmp" /usr/bin/fish "$tmp/home/.local/bin"

  local fish_file="$tmp/home/.config/fish/config.fish"
  if [[ ! -f "$fish_file" ]]; then
    echo "expected fish PATH config" >&2
    return 1
  fi
  if [[ "$(grep -cF 'fish_add_path --prepend --global --move "$HOME/.local/bin"' "$fish_file")" -ne 1 ]]; then
    echo "expected one canonical fish PATH entry" >&2
    cat "$fish_file" >&2
    return 1
  fi
}

test_bash_login_and_non_login_shells_resolve_canonical_path() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  _run_installer "$tmp" /bin/bash

  local login_resolved interactive_resolved
  login_resolved="$(
    HOME="$tmp/home" PATH="/usr/bin:/bin" \
      bash --login -c 'command -v multica'
  )"
  if [[ "$login_resolved" != "$tmp/home/.local/bin/multica" ]]; then
    echo "bash login shell resolved $login_resolved, expected canonical binary" >&2
    return 1
  fi

  interactive_resolved="$(
    HOME="$tmp/home" PATH="/usr/bin:/bin" \
      bash --noprofile --rcfile "$tmp/home/.bashrc" -ic 'command -v multica' 2>/dev/null
  )"
  if [[ "$interactive_resolved" != "$tmp/home/.local/bin/multica" ]]; then
    echo "bash non-login interactive shell resolved $interactive_resolved, expected canonical binary" >&2
    return 1
  fi
}

test_managed_block_moves_after_legacy_shadow_and_stays_idempotent() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  mkdir -p "$tmp/legacy-bin"
  cp "$tmp/payload/multica" "$tmp/legacy-bin/multica"
  cat >"$tmp/home/.zshrc" <<'RC'
export PATH="$HOME/.local/bin:$PATH"
export PATH="$HOME/legacy-bin:$PATH"
RC

  _run_installer "$tmp" /bin/zsh "$tmp/legacy-bin"
  _run_installer "$tmp" /bin/zsh "$tmp/home/.local/bin"

  local resolved
  resolved="$(
    HOME="$tmp/home" PATH="/usr/bin:/bin" \
      bash -c '. "$HOME/.zshrc"; command -v multica'
  )"
  if [[ "$resolved" != "$tmp/home/.local/bin/multica" ]]; then
    echo "persisted PATH still lets a later legacy path shadow canonical Multica" >&2
    cat "$tmp/home/.zshrc" >&2
    return 1
  fi
  if [[ "$(grep -cF 'export PATH="$HOME/.local/bin:$PATH"' "$tmp/home/.zshrc")" -ne 1 ]]; then
    echo "expected exactly one managed canonical PATH entry after rerun" >&2
    cat "$tmp/home/.zshrc" >&2
    return 1
  fi
}

test_unknown_shell_fallback_moves_canonical_path_last() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  mkdir -p "$tmp/legacy-bin"
  cp "$tmp/payload/multica" "$tmp/legacy-bin/multica"
  cat >"$tmp/home/.profile" <<'RC'
export PATH="$HOME/.local/bin:$PATH"
export PATH="$HOME/legacy-bin:$PATH"
RC

  _run_installer "$tmp" /bin/sh "$tmp/legacy-bin"

  local resolved
  resolved="$(
    HOME="$tmp/home" PATH="/usr/bin:/bin" \
      bash -c '. "$HOME/.profile"; command -v multica'
  )"
  if [[ "$resolved" != "$tmp/home/.local/bin/multica" ]]; then
    echo "fallback profile did not give canonical Multica final priority" >&2
    cat "$tmp/home/.profile" >&2
    return 1
  fi
}

test_managed_path_update_preserves_dotfile_symlink() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  mkdir -p "$tmp/home/dotfiles"
  printf '%s\n' 'export PATH="$HOME/legacy-bin:$PATH"' >"$tmp/home/dotfiles/zshrc"
  ln -s "dotfiles/zshrc" "$tmp/home/.zshrc"

  _run_installer "$tmp" /bin/zsh

  if [[ ! -L "$tmp/home/.zshrc" ]]; then
    echo "installer must preserve a symlinked shell config" >&2
    return 1
  fi
  if ! grep -qF 'export PATH="$HOME/.local/bin:$PATH"' "$tmp/home/dotfiles/zshrc"; then
    echo "expected managed path block in symlink target" >&2
    cat "$tmp/home/dotfiles/zshrc" >&2
    return 1
  fi
}

test_default_installs_only_to_user_local_without_sudo
test_same_version_legacy_path_is_migrated_to_user_local
test_fish_path_config_is_idempotent
test_bash_login_and_non_login_shells_resolve_canonical_path
test_managed_block_moves_after_legacy_shadow_and_stays_idempotent
test_unknown_shell_fallback_moves_canonical_path_last
test_managed_path_update_preserves_dotfile_symlink
echo "install.sh tests passed"
