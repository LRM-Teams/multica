import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "@multica/core/types";
import { runtimePickerOptions } from "./runtime-picker-options";

function runtime(
  id: string,
  daemonId: string,
  ownerId: string,
  visibility: "public" | "private",
): AgentRuntime {
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
    visibility,
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
  it("filters other private runtimes and keeps the current user's usable runtimes first", () => {
    const options = runtimePickerOptions(
      [
        runtime("teammate-a", "daemon-a", "teammate", "public"),
        runtime("mine", "daemon-b", "me", "private"),
        runtime("teammate-b", "daemon-c", "teammate", "private"),
      ],
      "me",
    );

    expect(options.map((option) => option.id)).toEqual(["mine", "teammate-a"]);
  });
});
