import type { AgentRuntime } from "../types";

/**
 * Shared AgentRuntime test fixture.
 *
 * Five test files in this directory had grown near-identical copies of this
 * object, so every new `agent_runtime` column meant editing all of them — the
 * same drift that made `attachAgentRuntimeNames` go stale server-side.
 * Defaults are deliberately unremarkable (online, healthy, owned by `user-1`)
 * so each test overrides only the fields its assertion actually depends on.
 */
export function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    current_version: "0.3.0",
    target_version: "0.4.0",
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-1",
    last_seen_at: "2026-07-03T00:00:00Z",
    created_at: "2026-07-03T00:00:00Z",
    updated_at: "2026-07-03T00:00:00Z",
    ...overrides,
  };
}
