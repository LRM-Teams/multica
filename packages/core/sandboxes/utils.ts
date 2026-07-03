import type { SandboxInstance } from "../types";

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
