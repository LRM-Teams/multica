#!/usr/bin/env bash
# Multica installer — installs the CLI and optionally provisions a self-host server.
#
# Install / upgrade CLI only:
#   curl -fsSL https://raw.githubusercontent.com/LRM-Teams/multica/main/scripts/install.sh | bash
#
# Install CLI + provision self-host server:
#   curl -fsSL https://raw.githubusercontent.com/LRM-Teams/multica/main/scripts/install.sh | bash -s -- --with-server
#
# After installation, run `multica setup` to configure your environment.
#
set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
REPO_URL="https://github.com/LRM-Teams/multica.git"
REPO_WEB_URL="https://github.com/LRM-Teams/multica"
RELEASE_REPO_WEB_URL="${MULTICA_RELEASE_REPO_WEB_URL:-$REPO_WEB_URL}"
INSTALL_SCRIPT_URL="https://raw.githubusercontent.com/LRM-Teams/multica/main/scripts/install.sh"
POWERSHELL_INSTALL_SCRIPT_URL="https://raw.githubusercontent.com/LRM-Teams/multica/main/scripts/install.ps1"
INSTALL_DIR="${MULTICA_INSTALL_DIR:-$HOME/.multica/server}"
CLI_BIN_DIR="$HOME/.local/bin"
CLI_PATH="$CLI_BIN_DIR/multica"

# Colors (disabled when not a terminal)
if [ -t 1 ] || [ -t 2 ]; then
  BOLD='\033[1m'
  GREEN='\033[0;32m'
  YELLOW='\033[0;33m'
  RED='\033[0;31m'
  CYAN='\033[0;36m'
  RESET='\033[0m'
else
  BOLD='' GREEN='' YELLOW='' RED='' CYAN='' RESET=''
fi

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
info()  { printf "${BOLD}${CYAN}==> %s${RESET}\n" "$*"; }
ok()    { printf "${BOLD}${GREEN}✓ %s${RESET}\n" "$*"; }
warn()  { printf "${BOLD}${YELLOW}⚠ %s${RESET}\n" "$*" >&2; }
fail()  { printf "${BOLD}${RED}✗ %s${RESET}\n" "$*" >&2; exit 1; }

command_exists() { command -v "$1" >/dev/null 2>&1; }

env_file_value() {
  local file="$1"
  local key="$2"
  local default="$3"
  local line value
  line="$(grep -E "^${key}=" "$file" 2>/dev/null | tail -n 1 || true)"
  if [ -z "$line" ]; then
    printf "%s" "$default"
    return
  fi
  value="${line#*=}"
  value="${value%$'\r'}"
  value="${value%\"}"
  value="${value#\"}"
  value="${value%\'}"
  value="${value#\'}"
  if [ -z "$value" ]; then
    printf "%s" "$default"
  else
    printf "%s" "$value"
  fi
}

selfhost_backend_port() {
  local file="${1:-.env}"
  local value
  for key in BACKEND_PORT API_PORT SERVER_PORT PORT; do
    value="$(env_file_value "$file" "$key" "")"
    if [ -n "$value" ]; then
      printf "%s" "$value"
      return
    fi
  done
  printf "8080"
}

selfhost_frontend_port() {
  env_file_value "${1:-.env}" "FRONTEND_PORT" "3000"
}

detect_os() {
  case "$(uname -s)" in
    Darwin) OS="darwin" ;;
    Linux)  OS="linux" ;;
    MINGW*|MSYS*|CYGWIN*)
            fail "This script does not support Windows. Use the PowerShell installer instead:
  irm ${POWERSHELL_INSTALL_SCRIPT_URL} | iex" ;;
    *)      fail "Unsupported operating system: $(uname -s). Multica supports macOS, Linux, and Windows." ;;
  esac

  ARCH="$(uname -m)"
  case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    arm64)   ARCH="arm64" ;;
    *)       fail "Unsupported architecture: $ARCH" ;;
  esac
}

# ---------------------------------------------------------------------------
# CLI Installation
# ---------------------------------------------------------------------------
install_cli_binary() {
  info "Installing Multica CLI from GitHub Releases..."

  # Get latest release tag
  local latest
  latest=$(curl -sI "$RELEASE_REPO_WEB_URL/releases/latest" 2>/dev/null | grep -i '^location:' | sed 's/.*tag\///' | tr -d '\r\n' || true)
  if [ -z "$latest" ]; then
    fail "Could not determine latest release. Check your network connection."
  fi

  local version="${latest#v}"
  local url="${RELEASE_REPO_WEB_URL}/releases/download/${latest}/multica-cli-${version}-${OS}-${ARCH}.tar.gz"
  local tmp_dir
  tmp_dir=$(mktemp -d)

  info "Downloading $url ..."
  if ! curl -fsSL "$url" -o "$tmp_dir/multica.tar.gz"; then
    rm -rf "$tmp_dir"
    fail "Failed to download CLI binary."
  fi

  tar -xzf "$tmp_dir/multica.tar.gz" -C "$tmp_dir" multica

  mkdir -p "$CLI_BIN_DIR"
  mv "$tmp_dir/multica" "$CLI_PATH"
  chmod +x "$CLI_PATH"
  prepend_to_path "$CLI_BIN_DIR"
  persist_cli_path

  rm -rf "$tmp_dir"
  ok "Multica CLI installed to $CLI_PATH"
}

prepend_to_path() {
  local dir="$1"
  local old_ifs="$IFS"
  local entry
  local rest=""
  IFS=:
  for entry in $PATH; do
    if [ "$entry" = "$dir" ]; then
      continue
    fi
    if [ -n "$rest" ]; then
      rest="$rest:$entry"
    else
      rest="$entry"
    fi
  done
  IFS="$old_ifs"
  export PATH="$dir${rest:+:$rest}"
  hash -r 2>/dev/null || true
}

write_managed_path_block() {
  local rc="$1"
  local line="$2"
  local start="# >>> Multica CLI PATH >>>"
  local end="# <<< Multica CLI PATH <<<"
  local tmp

  mkdir -p "$(dirname "$rc")"
  touch "$rc"
  tmp="$(mktemp)"
  awk -v start="$start" -v end="$end" -v line="$line" '
    BEGIN { in_block = 0; count = 0 }
    $0 == start { in_block = 1; next }
    in_block && $0 == end { in_block = 0; next }
    in_block { next }
    $0 == line { next }
    { lines[++count] = $0 }
    END {
      while (count > 0 && lines[count] == "") {
        count--
      }
      for (i = 1; i <= count; i++) {
        print lines[i]
      }
    }
  ' "$rc" >"$tmp"

  if [ -s "$tmp" ]; then
    printf '\n' >>"$tmp"
  fi
  printf '%s\n%s\n%s\n' "$start" "$line" "$end" >>"$tmp"
  cat "$tmp" >"$rc"
  rm -f "$tmp"
}

persist_cli_path() {
  local shell_name
  shell_name="$(basename "${SHELL:-}")"

  if [ "$shell_name" = "fish" ]; then
    write_managed_path_block \
      "$HOME/.config/fish/config.fish" \
      'fish_add_path --prepend --global --move "$HOME/.local/bin"'
    return
  fi

  local line='export PATH="$HOME/.local/bin:$PATH"'
  case "$shell_name" in
    zsh)
      write_managed_path_block "$HOME/.zshrc" "$line"
      ;;
    bash)
      local login_rc="$HOME/.bash_profile"
      if [ -f "$HOME/.bash_profile" ]; then
        login_rc="$HOME/.bash_profile"
      elif [ -f "$HOME/.bash_login" ]; then
        login_rc="$HOME/.bash_login"
      elif [ -f "$HOME/.profile" ]; then
        login_rc="$HOME/.profile"
      fi
      write_managed_path_block "$HOME/.bashrc" "$line"
      write_managed_path_block "$login_rc" "$line"
      ;;
    *)
      write_managed_path_block "$HOME/.profile" "$line"
      ;;
  esac
}

print_daemon_adoption_guidance() {
  local old_path="$1"
  warn "A daemon already started from $old_path keeps using that executable until it is restarted."
  warn "Do not interrupt active agent work. When all work on the profile is idle, adopt the canonical binary with:"
  printf '  "%s" daemon status\n' "$CLI_PATH" >&2
  printf '  "%s" daemon restart\n' "$CLI_PATH" >&2
  printf '  "%s" daemon status\n' "$CLI_PATH" >&2
  warn "For a named profile, put --profile before daemon, for example:"
  printf '  "%s" --profile staging daemon restart\n' "$CLI_PATH" >&2
  warn "Adoption is complete when daemon status reports the same Version as:"
  printf '  "%s" version\n' "$CLI_PATH" >&2
}

get_latest_version() {
  # grep exits 1 when no match; use `|| true` to avoid triggering pipefail
  curl -sI "$RELEASE_REPO_WEB_URL/releases/latest" 2>/dev/null | grep -i '^location:' | sed 's/.*tag\///' | tr -d '\r\n' || true
}

get_selfhost_ref() {
  if [ -n "${MULTICA_SELFHOST_REF:-}" ]; then
    printf '%s' "$MULTICA_SELFHOST_REF"
    return
  fi

  local latest
  latest=$(get_latest_version)
  if [ -n "$latest" ] && git ls-remote --exit-code --tags "$REPO_URL" "refs/tags/$latest" >/dev/null 2>&1; then
    printf '%s' "$latest"
    return
  fi

  printf '%s' "main"
}

checkout_server_ref() {
  local ref="$1"

  if [ "$ref" = "main" ]; then
    git fetch origin main --depth 1 2>/dev/null || true
    git checkout --force main 2>/dev/null || true
    git reset --hard origin/main 2>/dev/null || true
    return
  fi

  git fetch origin --tags --force 2>/dev/null || true
  if git rev-parse --verify --quiet "refs/tags/$ref" >/dev/null; then
    git checkout --force "$ref" 2>/dev/null || git checkout --force "tags/$ref" 2>/dev/null || true
    return
  fi

  git fetch origin "$ref" --depth 1 2>/dev/null || true
  git checkout --force "$ref" 2>/dev/null || true
}

pull_official_selfhost_images() {
  if docker compose -f docker-compose.selfhost.yml pull; then
    return
  fi

  echo ""
  warn "Official images for the selected self-host channel are not published yet."
  echo "This can happen before the first GHCR release is available."
  echo "From $INSTALL_DIR, build from source instead:"
  echo "  docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml up -d --build"
  exit 1
}

install_cli() {
  if command_exists multica; then
    local current_path current_ver
    current_path="$(command -v multica)"
    # `multica version` outputs "multica v0.1.13 (commit: abc1234)" — extract just the version
    current_ver=$(multica version 2>/dev/null | awk '{print $2}' || echo "unknown")

    local latest_ver
    latest_ver=$(get_latest_version)
    if [ -z "$latest_ver" ]; then
      fail "Could not determine latest release from ${RELEASE_REPO_WEB_URL}/releases/latest. Refusing to assume the installed CLI is current."
    fi

    # Normalize: strip leading 'v' for comparison
    local current_cmp="${current_ver#v}"
    local latest_cmp="${latest_ver#v}"

    if [ "$current_cmp" = "$latest_cmp" ] && [ "$current_path" = "$CLI_PATH" ]; then
      ok "Multica CLI is up to date ($current_ver)"
      prepend_to_path "$CLI_BIN_DIR"
      persist_cli_path
      return 0
    fi

    if [ "$current_path" != "$CLI_PATH" ]; then
      warn "Migrating Multica CLI from $current_path to the user-owned $CLI_PATH."
      warn "The old binary is no longer managed; remove it separately if it shadows $CLI_PATH in another shell."
    elif [ "$current_cmp" != "$latest_cmp" ]; then
      info "Multica CLI $current_ver installed, latest is $latest_ver — upgrading..."
    else
      info "Reinstalling Multica CLI $current_ver at the canonical user-owned path..."
    fi
    install_cli_binary

    local new_ver
    new_ver=$("$CLI_PATH" version 2>/dev/null | awk '{print $2}' || echo "unknown")
    ok "Multica CLI upgraded ($current_ver → $new_ver)"
    if [ "$current_path" != "$CLI_PATH" ]; then
      print_daemon_adoption_guidance "$current_path"
    fi
    return 0
  fi

  install_cli_binary

  # Verify
  if [ ! -x "$CLI_PATH" ] || [ "$(command -v multica 2>/dev/null || true)" != "$CLI_PATH" ]; then
    fail "CLI installed but 'multica' not found on PATH. You may need to restart your shell."
  fi
}

# ---------------------------------------------------------------------------
# Docker check
# ---------------------------------------------------------------------------
check_docker() {
  if ! command_exists docker; then
    printf "\n"
    fail "Docker is not installed. Multica self-hosting requires Docker and Docker Compose.

Install Docker:
  macOS:  https://docs.docker.com/desktop/install/mac-install/
  Linux:  https://docs.docker.com/engine/install/

After installing Docker, re-run this script with --with-server."
  fi

  if ! docker info >/dev/null 2>&1; then
    fail "Docker is installed but not running. Please start Docker and re-run this script."
  fi

  ok "Docker is available"
}

# ---------------------------------------------------------------------------
# Server setup (self-host / --with-server)
# ---------------------------------------------------------------------------
setup_server() {
  info "Setting up Multica server..."
  local server_ref
  server_ref=$(get_selfhost_ref)
  info "Using self-host assets from ${server_ref}..."

  if [ -d "$INSTALL_DIR/.git" ]; then
    info "Updating existing installation at $INSTALL_DIR..."
    cd "$INSTALL_DIR"
  else
    info "Cloning Multica repository..."
    if ! command_exists git; then
      fail "Git is not installed. Please install git and re-run."
    fi
    # Remove leftover directory from a previously interrupted clone
    if [ -d "$INSTALL_DIR" ]; then
      warn "Removing incomplete installation at $INSTALL_DIR..."
      rm -rf "$INSTALL_DIR"
    fi
    mkdir -p "$(dirname "$INSTALL_DIR")"
    git clone --depth 1 "$REPO_URL" "$INSTALL_DIR"
    cd "$INSTALL_DIR"
  fi

  checkout_server_ref "$server_ref"

  ok "Repository ready at $INSTALL_DIR ($server_ref)"

  # Generate .env if needed
  if [ ! -f .env ]; then
    info "Creating .env with random secrets..."
    cp .env.example .env
    local jwt pgpass
    jwt=$(openssl rand -hex 32)
    pgpass=$(openssl rand -hex 24)
    if [ "$(uname -s)" = "Darwin" ]; then
      sed -i '' "s/^JWT_SECRET=.*/JWT_SECRET=$jwt/" .env
      sed -i '' "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$pgpass/" .env
      sed -i '' -E "s#^(DATABASE_URL=postgres://[^:]+:)[^@]*(@.*)#\1$pgpass\2#" .env
    else
      sed -i "s/^JWT_SECRET=.*/JWT_SECRET=$jwt/" .env
      sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$pgpass/" .env
      sed -i -E "s#^(DATABASE_URL=postgres://[^:]+:)[^@]*(@.*)#\1$pgpass\2#" .env
    fi
    ok "Generated .env with random JWT_SECRET and POSTGRES_PASSWORD"
  else
    ok "Using existing .env"
  fi

  # Start Docker Compose
  info "Pulling official Multica images..."
  pull_official_selfhost_images
  info "Starting Multica services (this may take a few minutes on first run)..."
  docker compose -f docker-compose.selfhost.yml up -d

  # Wait for health check
  info "Waiting for backend to be ready..."
  local backend_port
  backend_port="$(selfhost_backend_port .env)"
  local ready=false
  for i in $(seq 1 45); do
    if curl -sf "http://localhost:${backend_port}/health" >/dev/null 2>&1; then
      ready=true
      break
    fi
    sleep 2
  done

  if [ "$ready" = true ]; then
    ok "Multica server is running"
  else
    warn "Server is still starting. You can check logs with:"
    echo "  cd $INSTALL_DIR && docker compose -f docker-compose.selfhost.yml logs"
    echo ""
  fi
}


# ---------------------------------------------------------------------------
# Main: Default mode (install / upgrade CLI only)
# ---------------------------------------------------------------------------
run_default() {
  printf "\n"
  printf "${BOLD}  Multica — Installer${RESET}\n"
  printf "\n"

  detect_os
  install_cli

  printf "\n"
  printf "${BOLD}${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}\n"
  printf "${BOLD}${GREEN}  ✓ Multica CLI is ready!${RESET}\n"
  printf "${BOLD}${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}\n"
  printf "\n"
  printf "  ${BOLD}Next: configure your environment${RESET}\n"
  printf "\n"
  printf "     ${CYAN}multica setup${RESET}                # Connect to Multica Cloud (multica.ai)\n"
  printf "     ${CYAN}multica setup self-host${RESET}       # Connect to a self-hosted server\n"
  printf "\n"
  printf "  ${BOLD}Self-hosting?${RESET} Install the server first:\n"
  printf "     curl -fsSL ${INSTALL_SCRIPT_URL} | bash -s -- --with-server\n"
  printf "\n"
}

# ---------------------------------------------------------------------------
# Main: With-server mode (provision self-host infrastructure + install CLI)
# ---------------------------------------------------------------------------
run_with_server() {
  printf "\n"
  printf "${BOLD}  Multica — Self-Host Installer${RESET}\n"
  printf "  Provisioning server infrastructure + installing CLI\n"
  printf "\n"

  detect_os
  check_docker
  setup_server
  install_cli

  printf "\n"
  printf "${BOLD}${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}\n"
  printf "${BOLD}${GREEN}  ✓ Multica server is running and CLI is ready!${RESET}\n"
  printf "${BOLD}${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}\n"
  printf "\n"
  local frontend_port backend_port
  frontend_port="$(selfhost_frontend_port "$INSTALL_DIR/.env")"
  backend_port="$(selfhost_backend_port "$INSTALL_DIR/.env")"
  printf "  ${BOLD}Frontend:${RESET}  http://localhost:%s\n" "$frontend_port"
  printf "  ${BOLD}Backend:${RESET}   http://localhost:%s\n" "$backend_port"
  printf "  ${BOLD}Server at:${RESET} %s\n" "$INSTALL_DIR"
  printf "\n"
  printf "  ${BOLD}Next: configure your CLI to connect${RESET}\n"
  printf "\n"
  printf "     ${CYAN}multica setup self-host${RESET}   # Configure + authenticate + start daemon\n"
  printf "\n"
  printf "  ${BOLD}Login:${RESET} configure ${CYAN}RESEND_API_KEY${RESET} in .env for email codes,\n"
  printf "  or read the generated code from backend logs when Resend is unset.\n"
  printf "\n"
  printf "  ${BOLD}To stop all services:${RESET}\n"
  printf "     curl -fsSL ${INSTALL_SCRIPT_URL} | bash -s -- --stop\n"
  printf "\n"
}

# ---------------------------------------------------------------------------
# Stop: shut down a self-hosted installation
# ---------------------------------------------------------------------------
run_stop() {
  printf "\n"
  info "Stopping Multica services..."

  if [ -d "$INSTALL_DIR" ]; then
    cd "$INSTALL_DIR"
    if [ -f docker-compose.selfhost.yml ]; then
      docker compose -f docker-compose.selfhost.yml down
      ok "Docker services stopped"
    else
      warn "No docker-compose.selfhost.yml found at $INSTALL_DIR"
    fi
  else
    warn "No Multica installation found at $INSTALL_DIR"
  fi

  if command_exists multica; then
    multica daemon stop 2>/dev/null && ok "Daemon stopped" || true
  fi

  printf "\n"
}

# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------
main() {
  local mode="default"

  while [ $# -gt 0 ]; do
    case "$1" in
      --with-server) mode="with-server" ;;
      --local)       mode="with-server" ;;  # backwards compat alias
      --stop)        mode="stop" ;;
      --help|-h)
        echo "Usage: install.sh [--with-server | --stop]"
        echo ""
        echo "  (default)       Install / upgrade the Multica CLI"
        echo "  --with-server   Install CLI + provision a self-host server (Docker)"
        echo "  --stop          Stop a self-hosted installation"
        echo ""
        echo "Environment variables:"
        echo "  MULTICA_INSTALL_DIR   Self-host server install directory"
        echo "                        (default: \$HOME/.multica/server)"
        echo "  MULTICA_SELFHOST_REF  Git ref to check out for self-host assets"
        echo "                        (default: latest release tag, falling back to main)"
        echo "  MULTICA_RELEASE_REPO_WEB_URL"
        echo "                        GitHub repo used for CLI release assets"
        echo "                        (default: $REPO_WEB_URL)"
        echo ""
        echo "After installation, run 'multica setup' to configure your environment."
        exit 0
        ;;
      *) warn "Unknown option: $1" ;;
    esac
    shift
  done

  case "$mode" in
    default)     run_default ;;
    with-server) run_with_server ;;
    stop)        run_stop ;;
  esac
}

main "$@"
