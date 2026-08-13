---
status: accepted
---

# Reuse a live resident process; do not hash-restart

Raft rebind keeps a live agent process and updates in-memory desired config.
Multica used to hash model/MCP/AGENTS on every acquire and implicit stop+start
on drift. That is not rebind: it minted a new process without an explicit
restart.

Decision: if the agent×runtime slot already holds a live process, reuse it.
Stop only on user restart/reset or process death. Changing model for immediate
effect uses the same explicit restart path as the Restart button.

Shipped runtimes are resident only (Claude Code, Codex, OpenCode, Pi, Cursor,
Kiro, Grok Build). One-shot CLI adapters are removed so the pool no longer
needs a resident/one-shot mode. The official product name is Grok Build; the
provider id and CLI remain `grok`.

Cost: a saved config that nobody restarted keeps running the old baked-in
model/MCP/AGENTS for another turn. Accepted; Raft's rebind has the same gap.
