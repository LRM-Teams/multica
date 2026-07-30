#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Mirrors install.sh's detect_os() so the fixture manifest advertises the
# same platform key the installer under test will look up.
_test_platform_key() {
  local os arch
  case "$(uname -s)" in
    Darwin) os="darwin" ;;
    Linux) os="linux" ;;
    *) os="linux" ;;
  esac
  arch="$(uname -m)"
  case "$arch" in
    x86_64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
  esac
  printf '%s-%s' "$os" "$arch"
}

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

  local sha256
  if command -v sha256sum >/dev/null 2>&1; then
    sha256="$(sha256sum "$tmp/multica.tar.gz" | awk '{print $1}')"
  else
    sha256="$(shasum -a 256 "$tmp/multica.tar.gz" | awk '{print $1}')"
  fi

  local platform
  platform="$(_test_platform_key)"
  cat >"$tmp/latest.json" <<JSON
{"tag":"v0.3.2","version":"0.3.2","platforms":{"${platform}":{"url":"https://cdn.leagent.me/computer/v0.3.2/multica-cli-0.3.2-${platform}.tar.gz","sha256":"${sha256}"}}}
JSON

  # install.sh's manifest parsing needs jq or python3 on PATH. Stage the
  # host's real one into stub-bin so the sandboxed PATH below finds it
  # regardless of where the host puts it (e.g. Homebrew's /opt/homebrew/bin
  # is not on this deliberately tight PATH).
  if command -v jq >/dev/null 2>&1; then
    ln -s "$(command -v jq)" "$stub_bin/jq"
  elif command -v python3 >/dev/null 2>&1; then
    ln -s "$(command -v python3)" "$stub_bin/python3"
  else
    echo "no jq or python3 available on this host to run install.sh tests" >&2
    exit 1
  fi

  cat >"$stub_bin/curl" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$MULTICA_TEST_CURL_LOG"
if [[ "$*" == *"/multica-ai/"* ]]; then
  echo "stub curl saw old upstream URL: $*" >&2
  exit 31
fi
if [[ "$*" == *"github.com"* ]]; then
  echo "stub curl saw a github.com URL, which 404s unauthenticated on this private repo: $*" >&2
  exit 32
fi

is_manifest_request=0
if [[ "$*" == *"latest.json"* ]]; then
  is_manifest_request=1
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
if [[ "$is_manifest_request" -eq 1 ]]; then
  cp "$MULTICA_TEST_MANIFEST" "$out"
else
  cp "$MULTICA_TEST_ARCHIVE" "$out"
fi
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
    MULTICA_TEST_MANIFEST="$tmp/latest.json" \
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
  if ! grep -q "https://cdn.leagent.me/computer/v0.3.2/multica-cli-0.3.2-" "$tmp/curl.log"; then
    echo "expected download from the leagent.me release feed, not GitHub" >&2
    cat "$tmp/curl.log" >&2 || true
    return 1
  fi
  if grep -q "github.com" "$tmp/curl.log"; then
    echo "installer must never hit github.com — it 404s unauthenticated on this private repo" >&2
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

test_checksum_mismatch_refuses_install() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  # Corrupt the manifest's advertised checksum so it no longer matches the
  # archive the stub curl serves — this must fail closed, not install an
  # unverified binary.
  sed -i.bak 's/"sha256":"[^"]*"/"sha256":"0000000000000000000000000000000000000000000000000000000000000000"/' "$tmp/latest.json"

  set +e
  HOME="$tmp/home" \
    SHELL=/bin/bash \
    PATH="$tmp/stub-bin:/usr/bin:/bin" \
    MULTICA_TEST_ARCHIVE="$tmp/multica.tar.gz" \
    MULTICA_TEST_MANIFEST="$tmp/latest.json" \
    MULTICA_TEST_CURL_LOG="$tmp/curl.log" \
    bash "$ROOT_DIR/scripts/install.sh" >"$tmp/install.out" 2>"$tmp/install.err"
  status=$?
  set -e

  if [[ "$status" -eq 0 ]]; then
    echo "installer must fail when the archive checksum does not match the manifest" >&2
    return 1
  fi
  if [[ -e "$tmp/home/.local/bin/multica" ]]; then
    echo "installer must not place a binary on checksum mismatch" >&2
    return 1
  fi
  if ! grep -qi "checksum" "$tmp/install.err"; then
    echo "expected an explicit checksum-verification failure message" >&2
    cat "$tmp/install.err" >&2
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
test_checksum_mismatch_refuses_install
echo "install.sh tests passed"
