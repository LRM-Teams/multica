import { describe, expect, it } from "vitest";
import {
  buildSandboxdConfigPath,
  buildSandboxdSetupCommand,
  effectiveSandboxNodeStatus,
  resolveSandboxdCubeSettings,
  SANDBOX_NODE_STALE_MS,
  SANDBOXD_PLACEHOLDER_TEMPLATE_ID,
  sanitizeSandboxdConfigSegment,
} from "./utils";

describe("effectiveSandboxNodeStatus", () => {
  const now = Date.parse("2026-07-03T12:00:00.000Z");

  it("returns stored status when not online", () => {
    expect(effectiveSandboxNodeStatus("offline", "2026-07-03T11:59:50.000Z", now)).toBe("offline");
  });

  it("returns online for a fresh last_seen_at", () => {
    expect(
      effectiveSandboxNodeStatus("online", "2026-07-03T11:59:50.000Z", now),
    ).toBe("online");
  });

  it("returns offline when last_seen_at exceeds the stale window", () => {
    const staleAt = new Date(now - SANDBOX_NODE_STALE_MS - 1).toISOString();
    expect(effectiveSandboxNodeStatus("online", staleAt, now)).toBe("offline");
  });

  it("returns offline when last_seen_at is missing", () => {
    expect(effectiveSandboxNodeStatus("online", null, now)).toBe("offline");
  });
});

describe("sanitizeSandboxdConfigSegment", () => {
  it("lowercases and collapses unsafe characters", () => {
    expect(sanitizeSandboxdConfigSegment(" Alice Doe ")).toBe("alice-doe");
    expect(sanitizeSandboxdConfigSegment("82.157.184.89:8090")).toBe("82.157.184.89-8090");
  });
});

describe("buildSandboxdConfigPath", () => {
  it("includes workspace, user handle, and server host:port", () => {
    expect(
      buildSandboxdConfigPath({
        workspaceSlug: "Acme",
        userName: "alice",
        serverUrl: "http://82.157.184.89:8090",
      }),
    ).toBe("~/.multica/sandbox_daemon/sandboxd-acme-alice-82.157.184.89-8090.json");
  });

  it("falls back to email local-part then user id prefix", () => {
    expect(
      buildSandboxdConfigPath({
        workspaceSlug: "ws",
        userEmail: "bob@example.com",
        serverUrl: "http://localhost:64832",
      }),
    ).toBe("~/.multica/sandbox_daemon/sandboxd-ws-bob-localhost-64832.json");

    expect(
      buildSandboxdConfigPath({
        workspaceSlug: "ws",
        userId: "160e59a2-e057-4770-994a-a26a7ffca8f9",
        serverUrl: "http://127.0.0.1:8080",
      }),
    ).toBe("~/.multica/sandbox_daemon/sandboxd-ws-160e59a2-127.0.0.1-8080.json");
  });
});

describe("resolveSandboxdCubeSettings", () => {
  it("uses placeholder template id when metadata is missing", () => {
    expect(resolveSandboxdCubeSettings(null)).toEqual({
      sandboxServer: "http://127.0.0.1:3000",
      cubeProxyHttp: "http://127.0.0.1",
      cubeDomain: "cube.app",
      cubeTemplateId: SANDBOXD_PLACEHOLDER_TEMPLATE_ID,
    });
  });

  it("prefers registered metadata values", () => {
    expect(
      resolveSandboxdCubeSettings({
        cube_api_url: "http://127.0.0.1:3000",
        cube_proxy_http: "http://127.0.0.1",
        cube_domain: "cube.app",
        cube_template_id: "tpl-17d5b530e726456691304136",
      }),
    ).toEqual({
      sandboxServer: "http://127.0.0.1:3000",
      cubeProxyHttp: "http://127.0.0.1",
      cubeDomain: "cube.app",
      cubeTemplateId: "tpl-17d5b530e726456691304136",
    });
  });
});

describe("buildSandboxdSetupCommand", () => {
  it("embeds registered template id in the setup command", () => {
    const { command, configPath } = buildSandboxdSetupCommand({
      serverUrl: "http://localhost:8080",
      nodeToken: "msn_token",
      nodeKey: "msk_key",
      name: "sandboxd-abc",
      ownerUserId: "user-1",
      workspaceSlug: "acme",
      userName: "alice",
      metadata: { cube_template_id: "tpl-real" },
    });
    expect(configPath).toBe("~/.multica/sandbox_daemon/sandboxd-acme-alice-localhost-8080.json");
    expect(command).toContain('"cube_template_id": "tpl-real"');
    expect(command).toContain("mkdir -p ~/.multica/sandbox_daemon");
    expect(command).toContain(`multica sandboxd --config ${configPath}`);
  });
});
