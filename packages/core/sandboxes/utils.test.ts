import { describe, expect, it } from "vitest";
import {
  buildSandboxdConfigPath,
  buildSandboxdSetupCommand,
  buildSandboxRuntimePayload,
  createEmptyRuntimeProviderEntry,
  effectiveSandboxNodeStatus,
  emptySandboxRuntimeForm,
  resolveCreateSandboxTemplate,
  resolveSandboxdCubeSettings,
  SANDBOX_NODE_STALE_MS,
  SANDBOXD_PLACEHOLDER_TEMPLATE_ID,
  sanitizeSandboxdConfigSegment,
  sandboxEndpointLinks,
  sandboxRuntimeForm,
} from "./utils";
import type { SandboxInstance } from "../types";

describe("sandboxEndpointLinks", () => {
  it("returns ordered service links and skips empty values", () => {
    expect(
      sandboxEndpointLinks({
        kind: "docker",
        term_url: "http://10.0.0.8:32768/term",
        pi_web_url: "http://10.0.0.8:32768/",
        novnc_url: " http://10.0.0.8:32769/ ",
        code_url: "",
      }),
    ).toEqual([
      { kind: "term", url: "http://10.0.0.8:32768/term" },
      { kind: "pi_web", url: "http://10.0.0.8:32768/" },
      { kind: "novnc", url: "http://10.0.0.8:32769/" },
    ]);
  });

  it("returns empty for missing endpoint_info", () => {
    expect(sandboxEndpointLinks(undefined)).toEqual([]);
    expect(sandboxEndpointLinks({})).toEqual([]);
  });
});

describe("resolveCreateSandboxTemplate", () => {
  it("maps empty and default to default", () => {
    expect(resolveCreateSandboxTemplate(undefined)).toBe("default");
    expect(resolveCreateSandboxTemplate(null)).toBe("default");
    expect(resolveCreateSandboxTemplate("")).toBe("default");
    expect(resolveCreateSandboxTemplate("  default  ")).toBe("default");
  });

  it("passes through an explicit template id (override)", () => {
    expect(resolveCreateSandboxTemplate("tpl-other")).toBe("tpl-other");
    expect(resolveCreateSandboxTemplate("  tpl-same-as-default  ")).toBe("tpl-same-as-default");
  });
});

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

function fakeInstance(runtime: Record<string, unknown>): SandboxInstance {
  return {
    id: "i1",
    workspace_id: "w1",
    creator_user_id: "u1",
    node_id: "n1",
    status: "running",
    template: "default",
    local_ref: "cube-1",
    endpoint_info: {},
    limits: {},
    metadata: { runtime },
    error: null,
    created_at: "2026-07-20T00:00:00Z",
    updated_at: "2026-07-20T00:00:00Z",
  };
}

describe("sandboxRuntimeForm / buildSandboxRuntimePayload", () => {
  it("returns empty form when runtime is missing", () => {
    const form = sandboxRuntimeForm(fakeInstance({}));
    expect(form.entries).toHaveLength(1);
    expect(form.entries[0]?.provider).toBe("openai");
    expect(buildSandboxRuntimePayload(form)).toBeUndefined();
  });

  it("parses legacy flat runtime", () => {
    const form = sandboxRuntimeForm(
      fakeInstance({
        api_key: "sk-1",
        base_url: "https://example.com/v1",
        model: "gpt-5.5",
      }),
    );
    expect(form.entries).toHaveLength(1);
    expect(form.entries[0]).toMatchObject({
      provider: "openai",
      apiKey: "sk-1",
      baseUrl: "https://example.com/v1",
      model: "gpt-5.5",
    });
    expect(buildSandboxRuntimePayload(form)).toMatchObject({
      providers: [
        {
          provider: "openai",
          api_key: "sk-1",
          base_url: "https://example.com/v1",
          model: "gpt-5.5",
        },
      ],
      default_provider: "openai",
      default_model: "gpt-5.5",
      api_key: "sk-1",
      model: "gpt-5.5",
    });
  });

  it("parses multi-provider runtime and keeps default selection", () => {
    const form = sandboxRuntimeForm(
      fakeInstance({
        providers: [
          { provider: "openai", api_key: "a", model: "gpt-5.5" },
          { provider: "anthropic", api_key: "b", model: "claude-sonnet" },
        ],
        default_provider: "anthropic",
        default_model: "claude-sonnet",
      }),
    );
    expect(form.entries).toHaveLength(2);
    const def = form.entries.find((e) => e.key === form.defaultKey);
    expect(def?.provider).toBe("anthropic");
    expect(def?.model).toBe("claude-sonnet");

    const second = createEmptyRuntimeProviderEntry("google");
    second.apiKey = "g";
    second.model = "gemini";
    const payload = buildSandboxRuntimePayload({
      entries: [...form.entries, second],
      defaultKey: form.defaultKey,
    });
    expect(payload?.providers).toHaveLength(3);
    expect(payload?.default_provider).toBe("anthropic");
    expect(payload?.default_model).toBe("claude-sonnet");
    expect(payload?.api_key).toBe("b");
  });

  it("fills the provider default model when credentials are present", () => {
    const form = emptySandboxRuntimeForm();
    const entry = form.entries[0];
    expect(entry).toBeDefined();
    entry!.apiKey = "sk-1";
    entry!.baseUrl = "https://example.com/v1";

    expect(buildSandboxRuntimePayload(form)).toMatchObject({
      providers: [
        {
          provider: "openai",
          api_key: "sk-1",
          base_url: "https://example.com/v1",
          model: "gpt-5.5",
        },
      ],
      default_provider: "openai",
      default_model: "gpt-5.5",
      model: "gpt-5.5",
    });
  });

  it("omits empty entries from payload", () => {
    const empty = emptySandboxRuntimeForm();
    empty.entries.push(createEmptyRuntimeProviderEntry("anthropic"));
    expect(buildSandboxRuntimePayload(empty)).toBeUndefined();
  });
});
