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
```

The CLI no longer exposes a `multica project` command. Use `workspace info
--projects` to inspect projects and their bound resources. Project creation and
administration are managed by the workspace UI/API.

For a sustained multi-agent channel Goal, the channel manager must establish
the code-delivery control plane before creating implementation Issues:

```bash
multica goal bootstrap --channel <group-id-or-name> \
  --project-title "<title>" \
  --repository-url "https://github.com/<owner>/<repo>.git" \
  --default-branch dev
```

This atomically creates or reuses the channel Project, attaches the canonical
`github_repo`, and binds the channel. It does not create vague placeholder
Issues: follow it with one channel-linked parent Issue and bounded child Issues
that carry acceptance criteria, distinct assignees, branches, and review.
The human channel manager can confirm the same Project/Git setup from the Goal
delivery control plane in the web or desktop UI. Always read the current Goal
and Project binding before bootstrap; if the UI already established it, reuse
that Project and continue with channel-linked Issues instead of creating a
competing delivery Project.

When the current task is bound to a project, inspect live bindings with
`multica workspace info --projects --output json`. Clone a `github_repo` into
this Agent workspace if it is not already present, then work inside that
checkout. Re-run the command when the binding may have changed — the runtime
brief is not updated when project resources change. Repository locations do
not belong in Agent memory or the runtime brief. Development conventions in a
checked-out repo's own `AGENTS.md` or `CLAUDE.md` still apply after the
checkout exists.

More source-backed details: `references/projects-source-map.md`.
