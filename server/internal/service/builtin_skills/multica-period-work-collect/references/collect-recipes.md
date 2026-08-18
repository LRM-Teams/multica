# Period Work collect — shell recipes

Use these on the **runtime OS**. Substitute `$SCAN_ROOT` (`$HOME` on local),
`$START` / `$END` (RFC3339 from the wake `<window>`).

## Discover git roots (bounded)

```bash
SCAN_ROOT="${HOME:-/root}"
find "$SCAN_ROOT" -maxdepth 5 \( \
  -name node_modules -o -name .next -o -name dist -o -name build -o \
  -name target -o -name vendor -o -name __pycache__ -o -name .cache -o \
  -name .ssh -o -name .gnupg -o -name .aws \
\) -prune -o -name .git -print 2>/dev/null | head -n 40 | sed 's|/.git$||'
```

If `find` is too slow or blocked, fall back to common project parents:

```bash
for d in "$HOME/code" "$HOME/src" "$HOME/work" "$HOME/projects" "$HOME/dev" "$HOME"; do
  [ -d "$d" ] || continue
  find "$d" -maxdepth 3 -name .git -print 2>/dev/null | head -n 20 | sed 's|/.git$||'
done
```

Skip any path under `.ssh`, `.gnupg`, `.aws`, or whose basename looks like
`.env` / credentials.

## Per-repo window harvest

```bash
REPO=/path/to/repo
START=2026-08-16T16:00:00Z
END=2026-08-23T16:00:00Z

git -C "$REPO" -c safe.directory="$REPO" remote -v

git -C "$REPO" -c safe.directory="$REPO" log --branches --no-patch \
  --after="$START" --before="$END" \
  --pretty=format:'%h %ci %an %s' | head -n 40

git -C "$REPO" -c safe.directory="$REPO" status --porcelain | head -n 80

git -C "$REPO" -c safe.directory="$REPO" diff --stat HEAD 2>/dev/null | head -n 40
```

## Short evidence (Highlights)

Pick a few commits or dirty files that matter:

```bash
git -C "$REPO" -c safe.directory="$REPO" show --stat --oneline -1 <hash>
git -C "$REPO" -c safe.directory="$REPO" show --format=fuller -1 <hash> -- <path> | head -n 80
git -C "$REPO" -c safe.directory="$REPO" diff -- <path> | head -n 80
```

Never dump an entire file or unbounded `git diff` without `head`.

## Integrated summary + Diagrams (after harvest)

After Repos / Highlights are filled from shell evidence:

1. Write **Integrated summary** themes for *this machine only*. Every theme
   must point back to at least one Highlight or repo line.
2. Add **Diagrams** only when a Mermaid flowchart / sequence / state chart
   clarifies multi-step work that bullets alone cannot. Keep diagrams small
   (≤ ~20 nodes). Do not invent nodes without evidence.

Diagrams are markdown Mermaid fenced blocks — no extra shell tools required.

## Runtime identity

```bash
hostname; uname -s; echo "HOME=$HOME"; pwd
```

## Delivery reminder

After the pack markdown is ready, send with `--note-write` to the pack page id
from the wake — do not edit the final Brief folder as the pack target.
