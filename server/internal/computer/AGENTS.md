# Computer boundary rules

The Computer package owns machine identity, bindings, resident lifecycle, and
host control. It does not own provider homes, provider-global skills, or
agent-workspace skill materialization; those belong to the daemon's execenv
layer.

Keep the two scopes separate:

- Provider-global configuration, authentication, caches, sessions, and skills
  stay in the inherited user/provider home (`CODEX_HOME`, `~/.codex/`,
  `~/.agents/`, or the equivalent provider home). Computer code must not copy,
  symlink, redirect, or clean them into an agent directory.
- Agent-specific skills are installed by the daemon below the provider
  process's `workingDirectory`, using the provider's workspace-native path.

In particular, Computer must never create or prescribe an agent-scoped
`codex-home` or `CODEX_HOME`. Changes to these paths must be implemented and
tested in `server/internal/daemon/execenv`, not in this package.
