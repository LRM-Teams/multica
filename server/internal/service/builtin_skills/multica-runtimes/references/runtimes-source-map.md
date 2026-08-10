# Runtimes source map

- `server/cmd/multica/cmd_runtime.go` registers runtime list, usage, and activity.
- `server/cmd/multica/cmd_computer.go` registers the machine-wide Computer lifecycle (`start`/`stop`/`restart`/`status`/`logs`), release-channel selection, `doctor` diagnostics, and upgrades.
- `server/cmd/multica/cmd_setup.go` validates the fixed production origin or explicit test origin, establishes one Workspace connection, and starts the resident.
- `server/internal/cli/service_environment.go` owns the production/test service-target contract; `release_channel.go` and `update.go` own independent `latest`/`alpha` manifest selection.
- `server/cmd/multica/cmd_daemon.go` registers the hidden, deprecated `daemon` lifecycle aliases that delegate to the same machine-wide Computer and emit deprecation guidance.
- `server/internal/computer` owns the machine-wide resident lifecycle (identity, environment-keyed Workspace connections, process, state layout, health, diagnostics) that the Computer and the hidden daemon aliases both drive.
- `server/cmd/server/router.go` registers daemon task claim, runtime APIs, canonical daemon upgrade APIs, and the temporary runtime update compatibility adapters.
- `server/internal/handler/runtime_update.go` resolves legacy runtime-scoped update requests to the runtime's daemon without recreating retired runtime update state.
- `server/internal/handler/machine_upgrade_handler.go` owns the canonical daemon Machine Upgrade lifecycle and its legacy runtime response projection.
- `server/internal/daemon/daemon.go` claims tasks, enters the durable Agent workspace, launches provider CLIs, and reports completion.
- `server/internal/daemon/execenv/agent_workspace.go` defines the canonical Agent workspace layout.
