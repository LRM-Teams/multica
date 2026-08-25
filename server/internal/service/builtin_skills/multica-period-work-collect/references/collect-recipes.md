# Period Work collect — shell recipes

Use these on the **runtime OS**. Substitute `$START` / `$END` (RFC3339 from
the wake `<window>`). **Always resolve `SCAN_ROOTS` first** — never scan
`$HOME` alone. Container sandboxes keep user work in `/workspace` (or `$PWD`
outside HOME), which is not under `$HOME`.

## Resolve scan roots (required, first)

```bash
# HOME is one root, not the only root. Skip agent-private .multica trees.
SCAN_ROOTS=""
add_scan_root() {
  d=$(cd "$1" 2>/dev/null && pwd -P) || return 0
  case "$d" in
    */.multica|*/.multica/*) return 0 ;;
  esac
  case " $SCAN_ROOTS " in
    *" $d "*) return 0 ;;
  esac
  SCAN_ROOTS="$SCAN_ROOTS $d"
}
add_scan_root "${HOME:-/root}"
add_scan_root /workspace
pwd_now=$(pwd -P 2>/dev/null || pwd)
add_scan_root "$pwd_now"
echo "SCAN_ROOTS=$SCAN_ROOTS"
```

Print `SCAN_ROOTS` into `## Runtime`. Then run every later recipe **once per
root**. An empty HOME with a populated `/workspace` is still a successful
scan, not an empty cloud box.

## Discover git roots (bounded)

```bash
for SCAN_ROOT in $SCAN_ROOTS; do
  find "$SCAN_ROOT" -maxdepth 5 \( \
    -name node_modules -o -name .next -o -name dist -o -name build -o \
    -name target -o -name vendor -o -name __pycache__ -o -name .cache -o \
    -name .ssh -o -name .gnupg -o -name .aws -o -name .multica -o \
    -name .cursor -o -name .pi \
  \) -prune -o -name .git -print 2>/dev/null | head -n 40 | sed 's|/.git$||'
done
```

If `find` is too slow or blocked, fall back to common project parents **plus
each scan root**:

```bash
for d in $SCAN_ROOTS "$HOME/code" "$HOME/src" "$HOME/work" "$HOME/projects" "$HOME/dev"; do
  [ -d "$d" ] || continue
  find "$d" -maxdepth 3 \( -name node_modules -o -name .multica \) -prune -o \
    -name .git -print 2>/dev/null | head -n 20 | sed 's|/.git$||'
done
```

Skip any path under `.ssh`, `.gnupg`, `.aws`, `.multica`, or whose basename
looks like `.env` / credentials.

## Non-git in-window files (required)

Git discovery misses a lone file under `/workspace`. After git roots, harvest
source-like files whose mtime is inside the window:

```bash
for SCAN_ROOT in $SCAN_ROOTS; do
  find "$SCAN_ROOT" -maxdepth 4 \( \
    -name node_modules -o -name .next -o -name dist -o -name build -o \
    -name target -o -name vendor -o -name __pycache__ -o -name .cache -o \
    -name .ssh -o -name .gnupg -o -name .aws -o -name .multica -o \
    -name .cursor -o -name .pi \
  \) -prune -o -type f \( \
    -name '*.py' -o -name '*.ts' -o -name '*.tsx' -o -name '*.js' -o \
    -name '*.go' -o -name '*.rs' -o -name '*.java' -o -name '*.md' -o \
    -name '*.c' -o -name '*.cpp' -o -name '*.h' \
  \) -newermt "$START" ! -newermt "$END" -print 2>/dev/null | head -n 80
done
```

Treat an in-window path (e.g. `/workspace/multica/hello_world.py`) as a
**Repos / roots** line and a Highlight even when there is no `.git`.

## Per-repo window harvest

```bash
REPO=/path/to/repo
START=2026-08-16T16:00:00Z
END=2026-08-23T16:00:00Z

git -C "$REPO" -c safe.directory="$REPO" remote -v

git -C "$REPO" -c safe.directory="$REPO" log --branches --no-patch \
  --after="$START" --before="$END" \
  --pretty=format:'%h %ci %an %s' | head -n 40

# Dirty paths: only keep files touched inside the window when possible.
git -C "$REPO" -c safe.directory="$REPO" status --porcelain | head -n 80
# Optional mtime filter for a dirty path (GNU find):
# find "$REPO/path" -newermt "$START" ! -newermt "$END" 2>/dev/null | head

git -C "$REPO" -c safe.directory="$REPO" diff --stat HEAD 2>/dev/null | head -n 40
```

**Do not** use `git log` without `--after` / `--before` for Highlights. If a
commit’s `%ci` is outside `$START`→`$END`, drop it.
## Short evidence (Highlights)

Pick a few commits or dirty files that matter:

```bash
git -C "$REPO" -c safe.directory="$REPO" show --stat --oneline -1 <hash>
git -C "$REPO" -c safe.directory="$REPO" show --format=fuller -1 <hash> -- <path> | head -n 80
git -C "$REPO" -c safe.directory="$REPO" diff -- <path> | head -n 80
```

Never dump an entire file or unbounded `git diff` without `head`. For a
non-git in-window file, `head -n 80` the file instead.

## Work groups + Diagrams (after harvest)

After Repos / Highlights are filled from shell evidence:

1. Write **`## Work groups`** for *this machine only*:
   - Default: one `###` group per git repo / project root.
   - Merge into one group when different repos/files share one outcome; state
     **why**.
   - Keep unrelated work in separate groups (never glue by calendar).
   - Every group item must point back to at least one Highlight or repo line.
2. Add **Diagrams** only when a Mermaid flowchart / sequence / state chart
   clarifies multi-step work that bullets alone cannot. Keep diagrams small
   (≤ ~20 nodes). Do not invent nodes without evidence.

Diagrams are markdown Mermaid fenced blocks — no extra shell tools required.

## Runtime identity

```bash
hostname; uname -s; echo "HOME=$HOME"; echo "PWD=$(pwd)"; echo "SCAN_ROOTS=$SCAN_ROOTS"
```

## Delivery reminder

After the pack markdown is ready, deliver with:

```bash
multica notes period-brief submit-pack --draft-page-id <draft-page-id>
```

Do not `--note-write` the pack into Notes. The draft id comes from the wake.
