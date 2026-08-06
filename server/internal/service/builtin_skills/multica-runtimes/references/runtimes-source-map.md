# Runtimes source map

- `server/cmd/multica/cmd_runtime.go` registers runtime list, usage, and activity.
- `server/cmd/server/router.go` registers daemon task claim, runtime APIs, and canonical daemon upgrade APIs.
- `server/internal/daemon/daemon.go` claims tasks, enters the durable Agent workspace, launches provider CLIs, and reports completion.
- `server/internal/daemon/execenv/agent_workspace.go` defines the canonical Agent workspace layout.
