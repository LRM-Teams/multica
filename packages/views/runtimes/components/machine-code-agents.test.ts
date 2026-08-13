// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "@multica/core/types";
import { KNOWN_PROVIDERS } from "./provider-logo";
import {
  codeAgentVersion,
  codeAgentVersionFromDeviceInfo,
  partitionMachineCodeAgents,
} from "./machine-code-agents";

function runtime(partial: Partial<AgentRuntime> & { provider: string }): AgentRuntime {
  return {
    id: `rt-${partial.provider}`,
    workspace_id: "ws",
    daemon_id: "daemon-1",
    name: partial.provider,
    runtime_mode: "local",
    launch_header: "",
    status: "online",
    device_info: "box",
    metadata: {},
    current_version: null,
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-1",
    last_seen_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...partial,
  };
}

describe("codeAgentVersion", () => {
  it("prefers metadata.version (CA) over current_version (Multica CLI)", () => {
    expect(
      codeAgentVersion(
        runtime({
          provider: "cursor",
          current_version: "0.3.94",
          metadata: { version: "1.2.3", cli_version: "0.3.94" },
        }),
      ),
    ).toBe("1.2.3");
  });

  it("falls back to device_info when metadata.version is missing", () => {
    expect(
      codeAgentVersionFromDeviceInfo("host.local · 2.1.121 (Claude Code)"),
    ).toBe("2.1.121");
    expect(
      codeAgentVersion(
        runtime({
          provider: "claude",
          current_version: "0.3.94",
          metadata: { cli_version: "0.3.94" },
          device_info: "s144 · 2.1.5 (Claude Code)",
        }),
      ),
    ).toBe("2.1.5");
  });

  it("does not treat Multica current_version as the CA version", () => {
    expect(
      codeAgentVersion(
        runtime({
          provider: "pi",
          current_version: "0.3.94",
          metadata: { cli_version: "0.3.94" },
          device_info: "host.local",
        }),
      ),
    ).toBeNull();
  });

  it("reduces provider-prefixed metadata.version to the version token (Grok)", () => {
    // Frank 2026-08-03: Grok registers metadata.version as
    // "grok 0.2.111 (94172f2aa4)"; rendering it under `v{{version}}`
    // read "vgrok 0.2.111 …" on the machine tile.
    expect(
      codeAgentVersion(
        runtime({
          provider: "grok",
          metadata: { version: "grok 0.2.111 (94172f2aa4)" },
          device_info: "ubuntu · grok 0.2.111 (94172f2aa4)",
        }),
      ),
    ).toBe("0.2.111");
  });
});

describe("partitionMachineCodeAgents", () => {
  it("only catalogs the six Frank/Iris providers", () => {
    expect(KNOWN_PROVIDERS.map((p) => p.id).sort()).toEqual(
      ["claude", "codex", "cursor", "grok", "opencode", "pi"].sort(),
    );
  });

  it("splits the catalog into installed vs not installed", () => {
    const { installed, notInstalled } = partitionMachineCodeAgents([
      runtime({
        provider: "cursor",
        current_version: "0.3.94",
        metadata: { version: "1.2.3", cli_version: "0.3.94" },
      }),
      runtime({
        provider: "claude",
        current_version: "0.3.94",
        metadata: { version: "2.1.5", cli_version: "0.3.94" },
      }),
    ]);

    expect(installed.map((row) => row.id).sort()).toEqual(["claude", "cursor"]);
    expect(installed.find((row) => row.id === "cursor")?.version).toBe("1.2.3");
    expect(installed.find((row) => row.id === "claude")?.label).toBe(
      "Claude Code",
    );
    expect(installed.find((row) => row.id === "cursor")?.docsUrl).toContain(
      "cursor.com",
    );

    expect(notInstalled.map((row) => row.id).sort()).toEqual(
      ["codex", "grok", "opencode", "pi"].sort(),
    );
  });

  it("lists every detected provider as installed, even outside the recommend catalog", () => {
    const { installed, notInstalled } = partitionMachineCodeAgents([
      runtime({ provider: "kiro", metadata: { version: "9.9.9" } }),
    ]);

    expect(installed.map((row) => row.id)).toEqual(["kiro"]);
    expect(installed.find((row) => row.id === "kiro")?.version).toBe("9.9.9");
    expect(installed.find((row) => row.id === "kiro")?.label).toBe("kiro");
    expect(notInstalled).toHaveLength(KNOWN_PROVIDERS.length);
    expect(notInstalled.map((row) => row.id)).not.toContain("kiro");
  });

  it("dedupes providers and keeps the first version seen", () => {
    const { installed } = partitionMachineCodeAgents([
      runtime({
        id: "a",
        provider: "pi",
        metadata: { version: "1.0.0" },
      }),
      runtime({
        id: "b",
        provider: "pi",
        metadata: { version: "2.0.0" },
      }),
    ]);

    expect(installed).toHaveLength(1);
    expect(installed[0]?.version).toBe("1.0.0");
  });
});
