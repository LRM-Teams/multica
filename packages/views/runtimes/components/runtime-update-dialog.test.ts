import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "@multica/core/types";
import {
  parseDismissedPromptKeys,
  runtimeUpdatePrompts,
  serializeDismissedPromptKeys,
} from "./runtime-update-prompt";

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "runtime-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Claude (dev-machine.local)",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "dev-machine.local · claude 1.0.0",
    metadata: { cli_version: "0.3.35" },
    current_version: "0.3.35",
    target_version: "0.3.36",
    update_state: "idle",
    runtime_health: "update_available",
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: "2026-07-03T03:00:00Z",
    created_at: "2026-07-03T03:00:00Z",
    updated_at: "2026-07-03T03:00:00Z",
    ...overrides,
  };
}

describe("runtimeUpdatePrompts", () => {
  it("dedupes multiple runtime rows on the same daemon and target version", () => {
    const first = runtimeUpdatePrompts([
      makeRuntime({ id: "rt-claude", provider: "claude" }),
      makeRuntime({ id: "rt-codex", provider: "codex" }),
    ]);
    const afterRuntimeListChange = runtimeUpdatePrompts([
      makeRuntime({ id: "rt-claude", provider: "claude" }),
    ]);

    expect(first).toHaveLength(1);
    expect(first[0]?.runtimes).toHaveLength(2);
    expect(first[0]?.key).toBe(afterRuntimeListChange[0]?.key);
  });

  it("prompts again when the target version changes", () => {
    const v036 = runtimeUpdatePrompts([
      makeRuntime({ id: "rt-claude", target_version: "0.3.36" }),
    ]);
    const v037 = runtimeUpdatePrompts([
      makeRuntime({ id: "rt-claude", target_version: "0.3.37" }),
    ]);

    expect(v036[0]?.key).not.toBe(v037[0]?.key);
  });

  it("keeps separate prompt keys for different daemons", () => {
    const prompts = runtimeUpdatePrompts([
      makeRuntime({ id: "rt-laptop", daemon_id: "daemon-laptop" }),
      makeRuntime({ id: "rt-desktop", daemon_id: "daemon-desktop" }),
    ]);

    expect(prompts).toHaveLength(2);
    expect(new Set(prompts.map((prompt) => prompt.key)).size).toBe(2);
  });

  it("falls back to the device name when daemon id is missing", () => {
    expect(
      runtimeUpdatePrompts([
        makeRuntime({
          daemon_id: null,
          name: "Claude (fallback-host.local)",
          device_info: "fallback-host.local · claude 1.0.0",
        }),
      ])[0]?.key,
    ).toContain("local:device:fallback-host.local:0.3.36");
  });
});

describe("dismissed runtime update prompt keys", () => {
  it("reads current JSON storage and legacy raw-key storage", () => {
    const current = parseDismissedPromptKeys(
      serializeDismissedPromptKeys(
        new Set(["daemon-a:0.3.36", "daemon-b:0.3.36"]),
      ),
    );
    const legacy = parseDismissedPromptKeys("daemon-a:0.3.36");

    expect(current.has("daemon-a:0.3.36")).toBe(true);
    expect(current.has("daemon-b:0.3.36")).toBe(true);
    expect(legacy.has("daemon-a:0.3.36")).toBe(true);
  });
});
