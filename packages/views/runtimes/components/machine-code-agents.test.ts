// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "@multica/core/types";
import { KNOWN_PROVIDERS } from "./provider-logo";
import {
  codeAgentVersionFromDeviceInfo,
  partitionMachineCodeAgents,
} from "./machine-code-agents";

function runtime(partial: Partial<AgentRuntime> & { provider: string }): AgentRuntime {
  return {
    id: partial.id ?? `rt-${partial.provider}`,
    workspace_id: "ws",
    daemon_id: "daemon-1",
    name: partial.name ?? partial.provider,
    runtime_mode: "local",
    provider: partial.provider,
    launch_header: "",
    status: "online",
    device_info: "box",
    metadata: {},
    current_version: partial.current_version ?? null,
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...partial,
  };
}

describe("codeAgentVersionFromDeviceInfo", () => {
  it("extracts the CA version from the device_info runtime half", () => {
    expect(
      codeAgentVersionFromDeviceInfo("host.local · 2.1.121 (Claude Code)"),
    ).toBe("2.1.121");
    expect(codeAgentVersionFromDeviceInfo("box · codex-cli 0.118.0")).toBe(
      "0.118.0",
    );
    expect(codeAgentVersionFromDeviceInfo("laptop · claude 1.0.0")).toBe(
      "1.0.0",
    );
  });

  it("does not fall back to Multica daemon versions", () => {
    expect(codeAgentVersionFromDeviceInfo("host.local")).toBeNull();
    expect(codeAgentVersionFromDeviceInfo(null)).toBeNull();
  });
});

describe("partitionMachineCodeAgents", () => {
  it("puts detected providers in installed and the rest of the catalog in notInstalled", () => {
    const { installed, notInstalled } = partitionMachineCodeAgents([
      runtime({
        provider: "cursor",
        // Multica daemon version — must NOT be shown as the CA version.
        current_version: "0.3.94",
        device_info: "s144 · cursor 1.2.3",
      }),
      runtime({
        provider: "claude",
        current_version: "0.3.94",
        device_info: "s144 · 2.1.5 (Claude Code)",
      }),
    ]);

    expect(installed.map((row) => row.id).sort()).toEqual(["claude", "cursor"]);
    expect(installed.find((row) => row.id === "cursor")?.version).toBe("1.2.3");
    expect(installed.find((row) => row.id === "claude")?.version).toBe("2.1.5");
    expect(installed.find((row) => row.id === "claude")?.label).toBe(
      "Claude Code",
    );

    const notIds = new Set(notInstalled.map((row) => row.id));
    expect(notIds.has("cursor")).toBe(false);
    expect(notIds.has("claude")).toBe(false);
    expect(notIds.has("codex")).toBe(true);
    expect(notInstalled).toHaveLength(KNOWN_PROVIDERS.length - 2);
  });

  it("keeps unknown installed providers visible even when not in the catalog", () => {
    const { installed, notInstalled } = partitionMachineCodeAgents([
      runtime({ provider: "mystery-cli" }),
    ]);

    expect(installed).toEqual([
      expect.objectContaining({
        id: "mystery-cli",
        label: "mystery-cli",
      }),
    ]);
    expect(notInstalled).toHaveLength(KNOWN_PROVIDERS.length);
  });

  it("dedupes providers and keeps the first version seen", () => {
    const { installed } = partitionMachineCodeAgents([
      runtime({
        id: "a",
        provider: "pi",
        device_info: "box · pi 1.0.0",
      }),
      runtime({
        id: "b",
        provider: "pi",
        device_info: "box · pi 2.0.0",
      }),
    ]);

    expect(installed).toHaveLength(1);
    expect(installed[0]?.version).toBe("1.0.0");
  });
});
