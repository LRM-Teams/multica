---
status: superseded
superseded_by: docs/adr/0019-runtime-agent-collectors-period-brief.md
---

# Computer Owner journals whole-machine work; Agent synthesizes a Period Work Brief note

> **Superseded (2026-08-17).** Period Work no longer uses Computer Host Digests
> as the machine-collection path. See ADR 0019: runtime Agents collect on the
> OS they run on; a dedicated Brief Agent synthesizes. Host Journal code may
> remain in-tree temporarily but is not the product contract for Briefs.

A Period Work Brief is a note the Computer Owner can show colleagues or a
manager: what they accomplished in a time window. It is not an activity dump,
not a PPT file, and not limited to work Multica dispatched. Platform facts
(issues, notes, runs, linked PRs) and a Machine Work Journal digest (git
commits and dirty-path traces under the Owner's home, with a denylist) are
gathered first, then one Agent filters and writes the brief into Notes.

This deliberately rejects three narrower designs: Agent-Workspace-only
roots, Multica-task completion summaries as the only local source, and
exporting slides. It also splits the old "C2" bundle: keymouse, screenshots,
clipboard, file bodies, secrets, and daemon diagnostics stay forbidden;
whole-machine *work traces* for the Computer Owner are in. Only that Owner
may enable the journal or read a digest. Unmatched local repos land in an
"unscoped local work" bucket so a Workspace brief does not leak unrelated
private projects as team narrative. Agent Daily is never a source. The
retrospective API stays deterministic facts; the Agent does the selection
and prose.
