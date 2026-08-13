# Projects source map

- `server/cmd/multica/cmd_project.go` registers project list, get, resource list/add/update/remove, create, update, delete, and status.
- `server/cmd/server/router.go` exposes project APIs, including `GET /api/agent/projects/{id}/resources`.
- `server/pkg/db/queries/project.sql` is the project query surface.
- Runtime brief teaches checkout-in-workspace and `workspace info --projects` in `server/internal/daemon/execenv/runtime_config.go` (`renderProjectContext`, Agent Memory Scope). `GET /api/projects?include_resources=true` (agent: `GET /api/agent/projects`) nests bound resources so the inventory stays live.
