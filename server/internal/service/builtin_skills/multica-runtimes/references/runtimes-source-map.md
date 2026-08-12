# Runtimes source map

- `server/cmd/multica/cmd_runtime.go` registers runtime list, usage, and activity.
- `server/cmd/multica/cmd_computer.go` registers the machine-wide Computer lifecycle (`start`/`stop`/`restart`/`status`/`logs`), release-channel selection, `doctor` diagnostics, and upgrades. Its retired-flag guard applies to lifecycle commands; setup reuses only the retired `--profile` check.
- `server/cmd/multica/cmd_setup.go` owns and accepts `--environment test --server-url <api-origin> --app-url <app-origin>`, reuses saved Test origins for repair setup, asks before switching an already-active environment, validates the target before side effects, establishes one Workspace connection with Workspace-scoped authorization, and starts the resident.
- `server/cmd/multica/cmd_config_use.go` switches an already-configured production/test environment together with its matching package, requires confirmation before interrupting a running resident, and supports explicit `--yes` automation.
- `server/internal/cli/service_environment.go` owns the production/test service-target contract; `release_channel.go` and `update.go` derive the matching stable/preview entry from the canonical `metainfo.json`.
- `server/cmd/multica/cmd_daemon.go` registers the hidden, deprecated `daemon` lifecycle aliases that delegate to the same machine-wide Computer and emit deprecation guidance.
- `server/internal/computer` owns the machine-wide resident lifecycle (identity, environment-keyed Workspace connections, process, state layout, health, diagnostics) that the Computer and the hidden daemon aliases both drive.
- `server/cmd/server/router.go` registers daemon task claim, runtime APIs, canonical daemon upgrade APIs, and the temporary runtime update compatibility adapters.
- `server/internal/handler/runtime_update.go` resolves legacy runtime-scoped update requests to the runtime's daemon without recreating retired runtime update state.
- `server/internal/handler/machine_upgrade_handler.go` owns the canonical daemon Machine Upgrade lifecycle and its legacy runtime response projection.
- `server/internal/daemon/machine_upgrade.go` and `machine_upgrade_recovery.go` own the durable local Machine Upgrade journal, exact rollback, and fail-closed startup recovery. Only a proven later Active generation may supersede a retained `candidate_ready` marker.
- `server/internal/daemon/daemon.go` claims tasks, enters the durable Agent workspace, launches provider CLIs, and reports completion.
- `server/internal/daemon/execenv/agent_workspace.go` defines the canonical Agent workspace layout.
