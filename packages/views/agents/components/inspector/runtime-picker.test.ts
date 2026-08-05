import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "@multica/core/types";
import { runtimePickerOptions } from "./runtime-picker-options";

function runtime(id: string, daemonId: string, ownerId: string): AgentRuntime {
  return {
    id,
    workspace_id: "workspace-1",
    daemon_id: daemonId,
    name: id,
    display_name: id,
    runtime_mode: "local",
    provider: "codex",
    status: "online",
    device_info: "Linux",
    metadata: {},
    owner_id: ownerId,
    last_seen_at: new Date().toISOString(),
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    current_version: "0.4.2",
    target_version: null,
    update_state: "idle",
    runtime_health: "ok",
    update_error: null,
  } as AgentRuntime;
}

describe("runtimePickerOptions", () => {
  it("includes every workspace runtime and keeps the current user's runtimes first", () => {
    const options = runtimePickerOptions(
      [
        runtime("teammate-a", "daemon-a", "teammate"),
        runtime("mine", "daemon-b", "me"),
        runtime("teammate-b", "daemon-c", "teammate"),
      ],
      "me",
    );

    expect(options.map((option) => option.id)).toEqual([
      "mine",
      "teammate-a",
      "teammate-b",
    ]);
  });
});
