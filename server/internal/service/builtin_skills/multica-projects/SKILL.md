---
name: multica-projects
description: "Use when creating, inspecting, updating, or debugging Multica projects and their issue membership."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Multica Projects

Projects group issues and provide a durable product-level identity.

```bash
multica project list --output json
multica project get <project-id> --output json
multica project create --title "<title>" --output json
multica project update <project-id> --title "<title>" --output json
multica project status <project-id> in_progress --output json
```

Project create, update, delete, and status commands mutate workspace state.
Repository locations and development conventions belong in Agent memory and in
the checked-out project's `AGENTS.md` or `CLAUDE.md`, not in repeated runtime
prompt metadata.

More source-backed details: `references/projects-source-map.md`.
