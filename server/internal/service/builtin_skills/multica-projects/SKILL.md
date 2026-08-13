---
name: multica-projects
description: "Use when creating, inspecting, updating, or debugging Multica projects and their issue membership."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Multica Projects

Projects group issues and provide a durable product-level identity.

```bash
multica workspace info --projects --output json
multica project list --output json
multica project get <project-id> --output json
multica project resource list <project-id> --output json
multica project create --title "<title>" --output json
multica project update <project-id> --title "<title>" --output json
multica project status <project-id> in_progress --output json
```

Project create, update, delete, and status commands mutate workspace state.

When the current task is bound to a project, inspect live bindings with
`multica workspace info --projects --output json`. Clone a `github_repo` into
this Agent workspace if it is not already present, then work inside that
checkout. Re-run the command when the binding may have changed — the runtime
brief is not updated when project resources change. Repository locations do
not belong in Agent memory or the runtime brief. Development conventions in a
checked-out repo's own `AGENTS.md` or `CLAUDE.md` still apply after the
checkout exists.

More source-backed details: `references/projects-source-map.md`.
