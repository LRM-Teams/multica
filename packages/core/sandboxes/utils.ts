import type { SandboxInstance, SandboxNodeStatus } from "../types";

/** Must stay in sync with sandboxNodeStaleThreshold in server/internal/handler/sandbox.go */
export const SANDBOX_NODE_STALE_MS = 30_000;

export function effectiveSandboxNodeStatus(
  status: SandboxNodeStatus,
  lastSeenAt: string | null | undefined,
  now = Date.now(),
): SandboxNodeStatus {
  if (status !== "online") return status;
  if (!lastSeenAt) return "offline";
  const lastSeenMs = new Date(lastSeenAt).getTime();
  if (Number.isNaN(lastSeenMs)) return "offline";
  return now - lastSeenMs > SANDBOX_NODE_STALE_MS ? "offline" : "online";
}

export function sandboxDisplayName(instance: SandboxInstance): string {
  const name = instance.metadata?.name;
  if (typeof name === "string" && name.trim()) {
    return name.trim();
  }
  return instance.template;
}

export function sandboxRuntime(instance: SandboxInstance): {
  apiKey: string;
  baseUrl: string;
  model: string;
} {
  const runtime = instance.metadata?.runtime;
  if (runtime && typeof runtime === "object" && !Array.isArray(runtime)) {
    const record = runtime as Record<string, unknown>;
    return {
      apiKey: typeof record.api_key === "string" ? record.api_key : "",
      baseUrl: typeof record.base_url === "string" ? record.base_url : "",
      model: typeof record.model === "string" ? record.model : "",
    };
  }
  return { apiKey: "", baseUrl: "", model: "" };
}

export function defaultSandboxName(): string {
  const suffix = Math.random().toString(36).slice(2, 8);
  return `sandbox-${suffix}`;
}

/** Sanitize a segment for use in a sandboxd config filename. */
export function sanitizeSandboxdConfigSegment(value: string): string {
  const cleaned = value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^[-.]+|[-.]+$/g, "");
  return cleaned || "unknown";
}

/**
 * Build a workspace/user/server-scoped sandboxd config path under the
 * sandbox server account home directory.
 *
 * Example: `~/.multica/sandbox_daemon/sandboxd-acme-alice-82.157.184.89-8090.json`
 */
export function buildSandboxdConfigPath(input: {
  workspaceSlug: string;
  userName?: string | null;
  userEmail?: string | null;
  userId?: string | null;
  serverUrl: string;
}): string {
  const userRaw =
    input.userName?.trim() ||
    input.userEmail?.split("@")[0]?.trim() ||
    input.userId?.trim()?.slice(0, 8) ||
    "user";

  let hostRaw = "server";
  try {
    const url = new URL(input.serverUrl);
    hostRaw = url.port ? `${url.hostname}-${url.port}` : url.hostname;
  } catch {
    hostRaw = input.serverUrl.replace(/^https?:\/\//, "").split("/")[0] || "server";
  }

  const slug = sanitizeSandboxdConfigSegment(input.workspaceSlug);
  const user = sanitizeSandboxdConfigSegment(userRaw);
  const host = sanitizeSandboxdConfigSegment(hostRaw);
  return `~/.multica/sandbox_daemon/sandboxd-${slug}-${user}-${host}.json`;
}

/** Placeholder used in first-time setup before sandboxd has registered. */
export const SANDBOXD_PLACEHOLDER_TEMPLATE_ID = "YOUR_CUBE_TEMPLATE_ID";

export type SandboxdCubeSettings = {
  sandboxServer: string;
  cubeProxyHttp: string;
  cubeDomain: string;
  cubeTemplateId: string;
};

function metadataString(
  metadata: Record<string, unknown> | null | undefined,
  key: string,
): string | null {
  const value = metadata?.[key];
  if (typeof value !== "string") return null;
  const trimmed = value.trim();
  return trimmed ? trimmed : null;
}

/**
 * Resolve Cube connection settings from sandbox node registration metadata.
 * Falls back to local defaults / placeholder template id when missing.
 */
export function resolveSandboxdCubeSettings(
  metadata?: Record<string, unknown> | null,
): SandboxdCubeSettings {
  return {
    sandboxServer: metadataString(metadata, "cube_api_url") ?? "http://127.0.0.1:3000",
    cubeProxyHttp: metadataString(metadata, "cube_proxy_http") ?? "http://127.0.0.1",
    cubeDomain: metadataString(metadata, "cube_domain") ?? "cube.app",
    cubeTemplateId:
      metadataString(metadata, "cube_template_id") ?? SANDBOXD_PLACEHOLDER_TEMPLATE_ID,
  };
}

export function buildSandboxdSetupCommand(input: {
  serverUrl: string;
  nodeToken: string;
  nodeKey: string;
  name: string;
  ownerUserId: string;
  workspaceSlug: string;
  userName?: string | null;
  userEmail?: string | null;
  userId?: string | null;
  metadata?: Record<string, unknown> | null;
  concurrency?: number;
  pollInterval?: string;
}): { configPath: string; command: string } {
  const cube = resolveSandboxdCubeSettings(input.metadata);
  const config = {
    server_url: input.serverUrl,
    node_token: input.nodeToken,
    node_key: input.nodeKey,
    name: input.name,
    owner_user_id: input.ownerUserId,
    sandbox_server: cube.sandboxServer,
    cube_proxy_http: cube.cubeProxyHttp,
    cube_domain: cube.cubeDomain,
    cube_template_id: cube.cubeTemplateId,
    concurrency: input.concurrency ?? 1,
    poll_interval: input.pollInterval ?? "5s",
  };
  const configPath = buildSandboxdConfigPath({
    workspaceSlug: input.workspaceSlug,
    userName: input.userName,
    userEmail: input.userEmail,
    userId: input.userId,
    serverUrl: input.serverUrl,
  });
  return {
    configPath,
    command: `mkdir -p ~/.multica/sandbox_daemon && cat > ${configPath} <<'EOF'
${JSON.stringify(config, null, 2)}
EOF
multica sandboxd --config ${configPath}`,
  };
}
