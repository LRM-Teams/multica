# Runtimes source map

- `server/cmd/multica/cmd_runtime.go` registers runtime list, usage, and activity.
- `server/cmd/server/router.go` registers daemon task claim, runtime APIs, canonical daemon upgrade APIs, and the temporary runtime update compatibility adapters.
- `server/internal/handler/runtime_update.go` resolves legacy runtime-scoped update requests to the runtime's daemon without recreating retired runtime update state.
- `server/internal/handler/machine_upgrade_handler.go` owns the canonical daemon Machine Upgrade lifecycle and its legacy runtime response projection.
- `server/internal/daemon/daemon.go` claims tasks, enters the durable Agent workspace, launches provider CLIs, and reports completion.
- `server/internal/daemon/execenv/agent_workspace.go` defines the canonical Agent workspace layout.
