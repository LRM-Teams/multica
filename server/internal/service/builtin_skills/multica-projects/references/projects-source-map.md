# Projects source map

- `server/cmd/multica/cmd_project.go` registers project list, get, resource list/add/update/remove, create, update, delete, and status.
- `server/cmd/server/router.go` exposes project APIs, including `GET /api/agent/projects/{id}/resources`, the agent Goal bootstrap route, and the human manager Goal bootstrap route.
- `server/internal/handler/agent_goal_bootstrap.go` owns the shared atomic Project/Git/channel bootstrap transaction for both agent and human channel managers.
- `packages/views/channels/components/channel-goal-card.tsx` provides the explicit human confirmation flow and only uses GitHub evidence to prefill the form.
- `server/pkg/db/queries/project.sql` is the project query surface.
- Runtime brief teaches checkout-in-workspace and `workspace info --projects` in `server/internal/daemon/execenv/runtime_config.go` (`renderProjectContext`, Agent Memory Scope). `GET /api/projects?include_resources=true` (agent: `GET /api/agent/projects`) nests bound resources so the inventory stays live.
