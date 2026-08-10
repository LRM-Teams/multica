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
if [[ "${1:-}" == "installer-activate" ]]; then
  printf '%s\n' "$*" >>"$MULTICA_TEST_ACTIVATE_LOG"
  launcher=""
  shift
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --launcher) launcher="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  mkdir -p "$(dirname "$launcher")"
  cp "$0" "$launcher"
  chmod +x "$launcher"
  exit 0
fi
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
  cat >"$tmp/production-manifest.json" <<JSON
{"tag":"v0.3.2","version":"0.3.2","platforms":{"${platform}":{"url":"https://cdn.leagent.me/computer/v0.3.2/multica-cli-0.3.2-${platform}.tar.gz","sha256":"${sha256}"}}}
JSON
  cat >"$tmp/test-manifest.json" <<JSON
{"tag":"v0.4.0-alpha.7","version":"0.4.0-alpha.7","platforms":{"${platform}":{"url":"https://cdn.leagent.me/computer/0.4.0-alpha.7/multica-cli-0.4.0-alpha.7-${platform}.tar.gz","sha256":"${sha256}"}}}
JSON
  printf '{"schema_version":1,"environments":{"production":' >"$tmp/metainfo.json"
  tr -d '\n' <"$tmp/production-manifest.json" >>"$tmp/metainfo.json"
  printf ',"test":' >>"$tmp/metainfo.json"
  tr -d '\n' <"$tmp/test-manifest.json" >>"$tmp/metainfo.json"
  printf '}}\n' >>"$tmp/metainfo.json"
  cp "$tmp/test-manifest.json" "$tmp/exact.json"

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

manifest_source=""
case "$*" in
  *"/computer/metainfo.json"*) manifest_source="$MULTICA_TEST_METAINFO" ;;
  *"/0.4.0-alpha.7/manifest.json"*) manifest_source="$MULTICA_TEST_EXACT_MANIFEST" ;;
esac

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
if [[ -n "$manifest_source" ]]; then
  cp "$manifest_source" "$out"
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
  local version_selector="${4:-}"
  local out="$tmp/install.out"
  local err="$tmp/install.err"
  local path="$tmp/stub-bin:/usr/bin:/bin"
  if [[ -n "$extra_path" ]]; then
    path="$extra_path:$path"
  fi

  local installer_args=()
  if [[ -n "$version_selector" ]]; then
    installer_args=(--version "$version_selector")
  fi

  if ! HOME="$tmp/home" \
    SHELL="$shell_path" \
    PATH="$path" \
    MULTICA_BIN_DIR="$tmp/must-not-be-used" \
    MULTICA_TEST_ARCHIVE="$tmp/multica.tar.gz" \
    MULTICA_TEST_PRODUCTION_MANIFEST="$tmp/production-manifest.json" \
    MULTICA_TEST_METAINFO="$tmp/metainfo.json" \
    MULTICA_TEST_TEST_MANIFEST="$tmp/test-manifest.json" \
    MULTICA_TEST_EXACT_MANIFEST="$tmp/exact.json" \
    MULTICA_TEST_CURL_LOG="$tmp/curl.log" \
    MULTICA_TEST_ACTIVATE_LOG="$tmp/activate.log" \
    bash "$ROOT_DIR/scripts/install.sh" "${installer_args[@]}" >"$out" 2>"$err"; then
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
  if ! grep -qF "installer-activate --version" "$tmp/activate.log"; then
    echo "installer must activate the verified candidate through VersionStore" >&2
    cat "$tmp/activate.log" >&2 || true
    return 1
  fi
  if ! grep -Eq "https://cdn\.leagent\.me/computer/[^ ]+/multica-cli-[^ ]+" "$tmp/curl.log"; then
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

_run_installer_expect_failure() {
  local tmp="$1"
  local version_selector="${2:-}"
  local status
  local installer_args=()
  if [[ -n "$version_selector" ]]; then
    installer_args=(--version "$version_selector")
  fi

  set +e
  HOME="$tmp/home" \
    SHELL=/bin/bash \
    PATH="$tmp/stub-bin:/usr/bin:/bin" \
    MULTICA_TEST_ARCHIVE="$tmp/multica.tar.gz" \
    MULTICA_TEST_PRODUCTION_MANIFEST="$tmp/production-manifest.json" \
    MULTICA_TEST_METAINFO="$tmp/metainfo.json" \
    MULTICA_TEST_TEST_MANIFEST="$tmp/test-manifest.json" \
    MULTICA_TEST_EXACT_MANIFEST="$tmp/exact.json" \
    MULTICA_TEST_CURL_LOG="$tmp/curl.log" \
    MULTICA_TEST_ACTIVATE_LOG="$tmp/activate.log" \
    bash "$ROOT_DIR/scripts/install.sh" "${installer_args[@]}" >"$tmp/install.out" 2>"$tmp/install.err"
  status=$?
  set -e

  if [[ "$status" -eq 0 ]]; then
    echo "installer unexpectedly succeeded" >&2
    cat "$tmp/install.out" >&2 || true
    cat "$tmp/install.err" >&2 || true
    return 1
  fi
  if [[ -e "$tmp/home/.local/bin/multica" ]]; then
    echo "a rejected manifest must not install a binary" >&2
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
  if ! grep -qF "\"$tmp/home/.local/bin/multica\" computer restart" "$tmp/install.err"; then
    echo "expected explicit idle Computer adoption command" >&2
    cat "$tmp/install.err" >&2
    return 1
  fi
  if grep -q -- "--profile\|daemon restart" "$tmp/install.err"; then
    echo "installer must not suggest profiles or the retired daemon command" >&2
    cat "$tmp/install.err" >&2
    return 1
  fi
}

test_version_selector_installs_test_environment() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  _run_installer "$tmp" /bin/zsh "" test

  if ! grep -qF "https://cdn.leagent.me/computer/metainfo.json" "$tmp/curl.log"; then
    echo "--version test must read canonical environment metainfo" >&2
    cat "$tmp/curl.log" >&2
    return 1
  fi
  if grep -qF "/latest.json" "$tmp/curl.log"; then
    echo "test selection must never fall back to legacy latest" >&2
    cat "$tmp/curl.log" >&2
    return 1
  fi
}

test_default_uses_canonical_manifest_not_legacy_latest_file() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  _run_installer "$tmp" /bin/zsh

  if ! grep -qF "https://cdn.leagent.me/computer/metainfo.json" "$tmp/curl.log"; then
    echo "the default installer must read canonical environment metainfo" >&2
    cat "$tmp/curl.log" >&2
    return 1
  fi
  if grep -qF "/latest.json" "$tmp/curl.log"; then
    echo "the new installer must not use the legacy latest.json name" >&2
    cat "$tmp/curl.log" >&2
    return 1
  fi
}

test_version_selector_installs_exact_immutable_release() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  _run_installer "$tmp" /bin/zsh "" v0.4.0-alpha.7

  if ! grep -qF "https://cdn.leagent.me/computer/0.4.0-alpha.7/manifest.json" "$tmp/curl.log"; then
    echo "an exact --version must read its immutable per-version manifest" >&2
    cat "$tmp/curl.log" >&2
    return 1
  fi
  if grep -Eq "/(latest|alpha)\.json" "$tmp/curl.log"; then
    echo "an exact version must not consult a movable pointer" >&2
    cat "$tmp/curl.log" >&2
    return 1
  fi
}

test_invalid_version_selector_fails_before_network() {
  local tmp status
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  set +e
  HOME="$tmp/home" \
    SHELL=/bin/bash \
    PATH="$tmp/stub-bin:/usr/bin:/bin" \
    MULTICA_TEST_ARCHIVE="$tmp/multica.tar.gz" \
    MULTICA_TEST_PRODUCTION_MANIFEST="$tmp/production-manifest.json" \
    MULTICA_TEST_METAINFO="$tmp/metainfo.json" \
    MULTICA_TEST_TEST_MANIFEST="$tmp/test-manifest.json" \
    MULTICA_TEST_EXACT_MANIFEST="$tmp/exact.json" \
    MULTICA_TEST_CURL_LOG="$tmp/curl.log" \
    bash "$ROOT_DIR/scripts/install.sh" --version nightly >"$tmp/install.out" 2>"$tmp/install.err"
  status=$?
  set -e

  if [[ "$status" -eq 0 ]]; then
    echo "an unsupported version selector must fail" >&2
    return 1
  fi
  if [[ -s "$tmp/curl.log" ]]; then
    echo "an invalid version selector must fail before any network request" >&2
    cat "$tmp/curl.log" >&2
    return 1
  fi
  if ! grep -qF "Invalid --version 'nightly'" "$tmp/install.err"; then
    echo "expected a useful invalid-version error" >&2
    cat "$tmp/install.err" >&2
    return 1
  fi
}

test_alpha_selector_is_not_retained() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  _run_installer_expect_failure "$tmp" alpha
  if ! grep -qF "Invalid --version 'alpha'" "$tmp/install.err"; then
    echo "alpha must not remain as a hidden test compatibility selector" >&2
    cat "$tmp/install.err" >&2
    return 1
  fi
  if [[ -s "$tmp/curl.log" ]]; then
    echo "the removed alpha selector must fail before any network request" >&2
    cat "$tmp/curl.log" >&2
    return 1
  fi
}

test_manifest_pointer_cannot_cross_release_channels() {
  local tmp

  tmp="$(mktemp -d)"
  _setup_sandbox "$tmp"
  jq '.environments.production = .environments.test' \
    "$tmp/metainfo.json" >"$tmp/metainfo.invalid.json"
  mv "$tmp/metainfo.invalid.json" "$tmp/metainfo.json"
  _run_installer_expect_failure "$tmp"
  if ! grep -qF "latest manifest must point to a stable" "$tmp/install.err"; then
    echo "expected latest-to-prerelease rejection" >&2
    cat "$tmp/install.err" >&2
    return 1
  fi
  rm -rf "$tmp"

  tmp="$(mktemp -d)"
  _setup_sandbox "$tmp"
  jq '.environments.test = .environments.production' \
    "$tmp/metainfo.json" >"$tmp/metainfo.invalid.json"
  mv "$tmp/metainfo.invalid.json" "$tmp/metainfo.json"
  _run_installer_expect_failure "$tmp" test
  if ! grep -qF "test environment must point to" "$tmp/install.err"; then
    echo "expected test-to-stable rejection" >&2
    cat "$tmp/install.err" >&2
    return 1
  fi
  rm -rf "$tmp"

  tmp="$(mktemp -d)"
  _setup_sandbox "$tmp"
  sed -i.bak 's/v0\.4\.0-alpha\.7/v0.4.0-alpha.8/g' "$tmp/exact.json"
  _run_installer_expect_failure "$tmp" v0.4.0-alpha.7
  if ! grep -qF "does not match requested v0.4.0-alpha.7" "$tmp/install.err"; then
    echo "expected exact-manifest tag mismatch rejection" >&2
    cat "$tmp/install.err" >&2
    return 1
  fi
  rm -rf "$tmp"
}

test_unsupported_metainfo_schema_fails_closed() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  _setup_sandbox "$tmp"
  jq '.schema_version = 2' "$tmp/metainfo.json" >"$tmp/metainfo.unsupported.json"
  mv "$tmp/metainfo.unsupported.json" "$tmp/metainfo.json"
  _run_installer_expect_failure "$tmp"

  if ! grep -qF "Could not resolve the selected release" "$tmp/install.err"; then
    echo "unsupported metainfo schema must fail before installation" >&2
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
  jq '.environments.production.platforms[] .sha256 = "0000000000000000000000000000000000000000000000000000000000000000"' \
    "$tmp/metainfo.json" >"$tmp/metainfo.corrupt.json"
  mv "$tmp/metainfo.corrupt.json" "$tmp/metainfo.json"

  set +e
  HOME="$tmp/home" \
    SHELL=/bin/bash \
    PATH="$tmp/stub-bin:/usr/bin:/bin" \
    MULTICA_TEST_ARCHIVE="$tmp/multica.tar.gz" \
    MULTICA_TEST_PRODUCTION_MANIFEST="$tmp/production-manifest.json" \
    MULTICA_TEST_METAINFO="$tmp/metainfo.json" \
    MULTICA_TEST_TEST_MANIFEST="$tmp/test-manifest.json" \
    MULTICA_TEST_EXACT_MANIFEST="$tmp/exact.json" \
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

test_powershell_installer_uses_same_version_store_bridge() {
  local script
  script="$(<"$ROOT_DIR/scripts/install.ps1")"
  for required in \
    '& $exeSrc installer-activate' \
    '--version $latest' \
    '--sha256 $binaryHash' \
    '--launcher $launcher'; do
    if ! grep -Fq -- "$required" <<<"$script"; then
      echo "PowerShell installer is missing shared VersionStore activation: $required" >&2
      return 1
    fi
  done
  if grep -Fq -- 'Copy-Item $exeSrc (Join-Path $binDir "multica.exe") -Force' <<<"$script"; then
    echo "PowerShell installer still overwrites the launcher outside VersionStore" >&2
    return 1
  fi
  if grep -Fq -- '@("latest", "test", "alpha")' <<<"$script"; then
    echo "PowerShell installer must not retain the alpha compatibility selector" >&2
    return 1
  fi
}

test_default_installs_only_to_user_local_without_sudo
test_default_uses_canonical_manifest_not_legacy_latest_file
test_same_version_legacy_path_is_migrated_to_user_local
test_version_selector_installs_test_environment
test_version_selector_installs_exact_immutable_release
test_invalid_version_selector_fails_before_network
test_alpha_selector_is_not_retained
test_manifest_pointer_cannot_cross_release_channels
test_unsupported_metainfo_schema_fails_closed
test_fish_path_config_is_idempotent
test_bash_login_and_non_login_shells_resolve_canonical_path
test_managed_block_moves_after_legacy_shadow_and_stays_idempotent
test_unknown_shell_fallback_moves_canonical_path_last
test_managed_path_update_preserves_dotfile_symlink
test_checksum_mismatch_refuses_install
test_powershell_installer_uses_same_version_store_bridge
echo "install.sh tests passed"
