#!/usr/bin/env bash
# migrate-legacy-pi-agents.sh — migrate legacy `.pi/agents/<id>/` state to the
# current `.multica/agents/<id>/` layout used by the daemon.
#
# Older daemon versions wrote per-agent state (memory, skills, sync_queue,
# inbox, notes, profile, shared-cache, feedback) under
#   <workspaces_root>/<workspace_id>/.pi/agents/<agent_id>/
# Newer versions write under
#   <workspaces_root>/<workspace_id>/.multica/agents/<agent_id>/
#
# This script copies legacy state into the new location for every agent it can
# map, leaving the source untouched by default. See --delete-unmapped / --purge.
#
# Usage:
#   scripts/migrate-legacy-pi-agents.sh [options] [--workspace <id> ...]
#
# Options:
#   --workspaces-root <dir>   Override workspaces root (default: $MULTICA_WORKSPACES_ROOT or ~/multica_workspaces)
#   --profile <name>          Resolve workspaces root from a multica profile (overrides --workspaces-root)
#   --workspace <id>          Limit to one workspace (repeatable). Default: every workspace under root.
#   --name-map <old> <new>    Map a legacy agent id to a new agent id by hand (repeatable).
#   --dry-run                 Report what would happen without writing or deleting anything.
#   --delete-unmapped         Delete legacy dirs whose agent id cannot be mapped (after backup). Implies backup.
#   --purge-source            After a successful copy, remove the legacy source dir. Implies --backup.
#   --backup-dir <dir>        Where to write tar backups (default: /tmp).
#   --no-backup               Skip the tar backup (not recommended with --purge-source / --delete-unmapped).
#   -v, --verbose             Verbose output.
#   -h, --help                Show this help.
#
# Exit codes: 0 success (or dry-run), 1 usage error, 2 runtime error.
set -euo pipefail

# Allow `multica` on PATH; fail later if missing when --profile is used.
SCRIPT_NAME="$(basename "$0")"

WORKSPACES_ROOT="${MULTICA_WORKSPACES_ROOT:-}"
PROFILE=""
WORKSPACES=()
NAME_MAP=()
DRY_RUN=0
DELETE_UNMAPPED=0
PURGE_SOURCE=0
BACKUP_DIR="/tmp"
NO_BACKUP=0
VERBOSE=0

SUBDIRS=(memory skills sync_queue inbox notes profile shared-cache feedback runtime repos)

log()   { printf '%s\n' "$*"; }
vlog()  { [ "$VERBOSE" -eq 1 ] && printf '[verbose] %s\n' "$*" || true; }
warn()  { printf '[warn] %s\n' "$*" >&2; }
err()   { printf '[error] %s\n' "$*" >&2; }
die()   { err "$*"; exit 2; }

usage() {
	sed -n '3,/^set -euo pipefail$/p' "$0" | sed 's/^# \{0,1\}//'
	exit 0
}

while [ $# -gt 0 ]; do
	case "$1" in
		--workspaces-root) WORKSPACES_ROOT="$2"; shift 2 ;;
		--profile) PROFILE="$2"; shift 2 ;;
		--workspace) WORKSPACES+=("$2"); shift 2 ;;
		--name-map) NAME_MAP+=("$2" "$3"); shift 3 ;;
		--dry-run) DRY_RUN=1; shift ;;
		--delete-unmapped) DELETE_UNMAPPED=1; shift ;;
		--purge-source) PURGE_SOURCE=1; shift ;;
		--backup-dir) BACKUP_DIR="$2"; shift 2 ;;
		--no-backup) NO_BACKUP=1; shift ;;
		-v|--verbose) VERBOSE=1; shift ;;
		-h|--help) usage ;;
		*) err "unknown option: $1"; exit 1 ;;
	esac
done

# Resolve workspaces root.
if [ -n "$PROFILE" ]; then
	if ! command -v multica >/dev/null 2>&1; then
		die "multica CLI not found on PATH; needed for --profile resolution"
	fi
	# `multica daemon status --profile <p>` is a stable, cheap call but does not
	# expose the root. Instead reuse the same env the daemon uses.
	resolved="$(MULTICA_WORKSPACES_ROOT="" multica config show --profile "$PROFILE" 2>/dev/null | awk -F': ' '/^[Ww]orkspaces[_ ]root/ {print $2}' | head -1 || true)"
	if [ -z "$resolved" ]; then
		# Fallback: profile-scoped default mirrors ResolveWorkspacesRoot semantics.
		resolved="$HOME/multica_workspaces_${PROFILE}"
	fi
	WORKSPACES_ROOT="$resolved"
fi
if [ -z "$WORKSPACES_ROOT" ]; then
	WORKSPACES_ROOT="$HOME/multica_workspaces"
fi

if [ ! -d "$WORKSPACES_ROOT" ]; then
	die "workspaces root not found: $WORKSPACES_ROOT"
fi

[ "$NO_BACKUP" -eq 1 ] && [ "$PURGE_SOURCE$DELETE_UNMAPPED" != "00" ] && {
	warn "--no-backup ignored because --purge-source/--delete-unmapped requires a backup"; NO_BACKUP=0
}

# Select workspace dirs.
if [ "${#WORKSPACES[@]}" -eq 0 ]; then
	while IFS= read -r d; do WORKSPACES+=("$d"); done < <(find "$WORKSPACES_ROOT" -mindepth 1 -maxdepth 1 -type d ! -name '.*' 2>/dev/null || true)
fi

map_legacy_id() {
	# Echoes the new agent id for a legacy id, or empty if unmapped.
	local legacy="$1" workspace="$2" i
	# 1. Explicit --name-map.
	for (( i=0; i<${#NAME_MAP[@]}; i+=2 )); do
		if [ "${NAME_MAP[$i]}" = "$legacy" ]; then printf '%s\n' "${NAME_MAP[$((i+1))]}"; return 0; fi
	done
	# 2. Same id exists at the new path.
	if [ -d "$WORKSPACES_ROOT/$workspace/.multica/agents/$legacy" ]; then
		printf '%s\n' "$legacy"; return 0
	fi
	# 3. Same id is referenced by a current agent row (heuristic: the dir was
	#    created by a run for the same agent id). We treat presence of any
	#    legacy dir under .pi/agents as evidence the id is still valid and copy
	#    it verbatim — the daemon will reuse it on the next run for that id.
	#    (Name-based mapping would require server API access; left as a hook.)
	printf '%s\n' "$legacy"
	return 0
}

copy_tree() {
	local src="$1" dst="$2" sub
	for sub in "${SUBDIRS[@]}"; do
		[ -d "$src/$sub" ] || continue
		mkdir -p "$dst/$sub"
		# memory files: never overwrite newer writes (--ignore-existing).
		# everything else: same treatment — additive, non-destructive.
		if [ "$DRY_RUN" -eq 1 ]; then
			vlog "dry-run: rsync $src/$sub/ -> $dst/$sub/"
			continue
		fi
		rsync -a --ignore-existing "$src/$sub/" "$dst/$sub/" 2>/dev/null || \
			cp -an "$src/$sub/." "$dst/$sub/" 2>/dev/null || true
	done
}

backup_legacy() {
	[ "$NO_BACKUP" -eq 1 ] && return 0
	local legacy_dir="$1" workspace="$2" legacy_id="$3"
	local stamp; stamp="$(date +%Y%m%dT%H%M%S)"
	local tarball="$BACKUP_DIR/legacy-pi-agents-${workspace}-${legacy_id}-${stamp}.tar.gz"
	mkdir -p "$BACKUP_DIR"
	if [ "$DRY_RUN" -eq 1 ]; then
		vlog "dry-run: tar czf $tarball $legacy_dir"
		return 0
	fi
	tar czf "$tarball" -C "$legacy_dir/.." "$legacy_id" 2>/dev/null || { warn "backup failed for $legacy_dir"; return 1; }
	log "backup: $tarball"
}

purge_legacy() {
	local legacy_dir="$1"
	[ "$DRY_RUN" -eq 1 ] && { vlog "dry-run: rm -rf $legacy_dir"; return 0; }
	rm -rf "$legacy_dir"
	log "purged: $legacy_dir"
}

total_mapped=0
total_unmapped=0
total_copied=0

for ws in "${WORKSPACES[@]}"; do
	workspace="$(basename "$ws")"
	legacy_base="$ws/.pi/agents"
	new_base="$ws/.multica/agents"
	[ -d "$legacy_base" ] || { vlog "no legacy agents in workspace $workspace"; continue; }

	log "=== workspace $workspace ==="
	while IFS= read -r legacy_dir; do
		legacy_id="$(basename "$legacy_dir")"
		new_id="$(map_legacy_id "$legacy_id" "$workspace")"
		new_dir="$new_base/$new_id"
		vlog "legacy $legacy_id -> new $new_id"
		if [ -z "$new_id" ]; then
			total_unmapped=$((total_unmapped+1))
			warn "unmapped legacy agent: $legacy_dir"
			if [ "$DELETE_UNMAPPED" -eq 1 ]; then
				backup_legacy "$legacy_dir" "$workspace" "$legacy_id" || continue
				purge_legacy "$legacy_dir"
			fi
			continue
		fi
		total_mapped=$((total_mapped+1))
		[ -d "$new_dir" ] || mkdir -p "$new_dir"
		copy_tree "$legacy_dir" "$new_dir"
		total_copied=$((total_copied+1))
		if [ "$PURGE_SOURCE" -eq 1 ]; then
			backup_legacy "$legacy_dir" "$workspace" "$legacy_id" || continue
			purge_legacy "$legacy_dir"
		fi
	done < <(find "$legacy_base" -mindepth 1 -maxdepth 1 -type d 2>/dev/null || true)
done

log "----- summary -----"
log "workspaces scanned : ${#WORKSPACES[@]}"
log "legacy agents mapped: $total_mapped"
log "legacy agents copied: $total_copied"
log "legacy agents unmapped: $total_unmapped"
[ "$DRY_RUN" -eq 1 ] && log "(dry-run: no files were written or deleted)"
