import type {
  SandboxInstance,
  SandboxNodeStatus,
  SandboxRuntimeConfig,
  SandboxRuntimeProviderConfig,
} from "../types";

/**
 * Server/job liveness window. Must stay in sync with
 * sandboxNodeStaleThreshold in server/internal/handler/sandbox.go.
 *
 * UI code should prefer the API's already-computed status field. Re-applying
 * this window against a cached last_seen_at (plus a local clock tick) falsely
 * flips nodes offline between refetches.
 */
export const SANDBOX_NODE_STALE_MS = 60_000;

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

/** Common Pi provider names offered in the sandbox runtime form. */
export const SANDBOX_RUNTIME_PROVIDER_PRESETS = ["openai", "anthropic", "google"] as const;

const SANDBOX_RUNTIME_DEFAULT_MODELS: Record<string, string> = {
  openai: "gpt-5.5",
  anthropic: "claude-sonnet-4.6",
  google: "gemini-3.1-pro",
};

function defaultSandboxRuntimeModel(provider: string): string {
  return SANDBOX_RUNTIME_DEFAULT_MODELS[provider.trim().toLowerCase()] ?? "";
}

export type SandboxRuntimeProviderFormEntry = {
  /** Stable React key; not persisted. */
  key: string;
  provider: string;
  apiKey: string;
  baseUrl: string;
  model: string;
};

export type SandboxRuntimeFormState = {
  entries: SandboxRuntimeProviderFormEntry[];
  /** `key` of the entry used as Pi defaultProvider / defaultModel. */
  defaultKey: string;
};

let runtimeEntrySeq = 0;

export function createEmptyRuntimeProviderEntry(
  provider = "openai",
): SandboxRuntimeProviderFormEntry {
  runtimeEntrySeq += 1;
  return {
    key: `rt-${runtimeEntrySeq}-${Math.random().toString(36).slice(2, 8)}`,
    provider,
    apiKey: "",
    baseUrl: "",
    model: "",
  };
}

export function emptySandboxRuntimeForm(): SandboxRuntimeFormState {
  const entry = createEmptyRuntimeProviderEntry("openai");
  return { entries: [entry], defaultKey: entry.key };
}

function asTrimmedString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function providerEntryFromRecord(
  record: Record<string, unknown>,
  fallbackProvider = "openai",
): SandboxRuntimeProviderFormEntry {
  return {
    ...createEmptyRuntimeProviderEntry(asTrimmedString(record.provider) || fallbackProvider),
    apiKey: asTrimmedString(record.api_key),
    baseUrl: asTrimmedString(record.base_url),
    model: asTrimmedString(record.model),
  };
}

function providerEntryHasContent(entry: SandboxRuntimeProviderFormEntry): boolean {
  return Boolean(entry.apiKey.trim() || entry.baseUrl.trim() || entry.model.trim());
}

/**
 * Load editable runtime form state from sandbox metadata.
 * Supports the multi-provider shape and legacy flat api_key/base_url/model.
 */
export function sandboxRuntimeForm(instance: SandboxInstance): SandboxRuntimeFormState {
  const runtime = instance.metadata?.runtime;
  if (!runtime || typeof runtime !== "object" || Array.isArray(runtime)) {
    return emptySandboxRuntimeForm();
  }
  const record = runtime as Record<string, unknown>;

  if (Array.isArray(record.providers) && record.providers.length > 0) {
    const entries: SandboxRuntimeProviderFormEntry[] = [];
    for (const item of record.providers) {
      if (!item || typeof item !== "object" || Array.isArray(item)) continue;
      const entry = providerEntryFromRecord(item as Record<string, unknown>);
      if (providerEntryHasContent(entry)) entries.push(entry);
    }
    if (entries.length === 0) return emptySandboxRuntimeForm();

    const defaultProvider = asTrimmedString(record.default_provider);
    const defaultModel = asTrimmedString(record.default_model);
    const fallback = entries[0];
    if (!fallback) return emptySandboxRuntimeForm();
    const matched =
      entries.find(
        (e) =>
          e.provider === defaultProvider &&
          (!defaultModel || e.model === defaultModel || !e.model),
      ) ??
      entries.find((e) => e.provider === defaultProvider) ??
      fallback;

    return { entries, defaultKey: matched.key };
  }

  const legacy = providerEntryFromRecord(record);
  if (!providerEntryHasContent(legacy)) return emptySandboxRuntimeForm();
  return { entries: [legacy], defaultKey: legacy.key };
}

/**
 * @deprecated Use sandboxRuntimeForm. Returns the default entry's flat fields.
 */
export function sandboxRuntime(instance: SandboxInstance): {
  apiKey: string;
  baseUrl: string;
  model: string;
} {
  const form = sandboxRuntimeForm(instance);
  const entry = form.entries.find((e) => e.key === form.defaultKey) ?? form.entries[0];
  if (!entry) return { apiKey: "", baseUrl: "", model: "" };
  return { apiKey: entry.apiKey, baseUrl: entry.baseUrl, model: entry.model };
}

/**
 * Build the API runtime payload from the form. Returns undefined when empty
 * so callers can omit the field entirely.
 */
export function buildSandboxRuntimePayload(
  form: SandboxRuntimeFormState,
): SandboxRuntimeConfig | undefined {
  const providers: SandboxRuntimeProviderConfig[] = [];
  for (const entry of form.entries) {
    const provider = entry.provider.trim() || "openai";
    const apiKey = entry.apiKey.trim();
    const baseUrl = entry.baseUrl.trim();
    const explicitModel = entry.model.trim();
    // Require at least one concrete config field so an empty create form
    // does not enqueue a no-op reconfigure.
    if (!apiKey && !baseUrl && !explicitModel) continue;
    const model = explicitModel || defaultSandboxRuntimeModel(provider);
    const item: SandboxRuntimeProviderConfig = { provider };
    if (apiKey) item.api_key = apiKey;
    if (baseUrl) item.base_url = baseUrl;
    if (model) item.model = model;
    providers.push(item);
  }
  if (providers.length === 0) return undefined;

  const firstProvider = providers[0];
  if (!firstProvider) return undefined;

  const defaultFormEntry =
    form.entries.find((e) => e.key === form.defaultKey) ?? form.entries[0];
  const defaultProviderName = (
    defaultFormEntry?.provider.trim() || firstProvider.provider
  ).trim();
  const defaultPayload =
    providers.find((p) => p.provider === defaultProviderName) ?? firstProvider;

  return {
    providers,
    default_provider: defaultPayload.provider,
    default_model: defaultPayload.model ?? "",
    // Legacy flat fields from the default entry (old Cube templates).
    provider: defaultPayload.provider,
    ...(defaultPayload.api_key ? { api_key: defaultPayload.api_key } : {}),
    ...(defaultPayload.base_url ? { base_url: defaultPayload.base_url } : {}),
    ...(defaultPayload.model ? { model: defaultPayload.model } : {}),
  };
}

export function defaultSandboxName(): string {
  const suffix = Math.random().toString(36).slice(2, 8);
  return `sandbox-${suffix}`;
}

/** Default name offered when creating a Cube snapshot template from an instance. */
export function defaultSandboxSnapshotName(instance: SandboxInstance): string {
  const base = sandboxDisplayName(instance).trim() || "sandbox";
  const stamp = new Date().toISOString().slice(0, 16).replace("T", "-").replace(":", "");
  return `${base}-snap-${stamp}`;
}

/**
 * Resolve the `template` field for POST /api/sandboxes.
 *
 * - Empty / `"default"` → `"default"` (sandboxd uses the node's configured cube_template_id).
 * - Any other non-empty value → pass through as an explicit Cube template ID override
 *   (higher priority than the node default, even when it equals the current default).
 */
export function resolveCreateSandboxTemplate(selected: string | undefined | null): string {
  const trimmed = (selected ?? "").trim();
  if (!trimmed || trimmed === "default") {
    return "default";
  }
  return trimmed;
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
