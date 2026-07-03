import { runtimeTargetVersion } from "@multica/core/runtimes";
import type { AgentRuntime } from "@multica/core/types";

// Bump this key when we intentionally want every eligible user to see the
// daemon update prompt again even if they dismissed an earlier rollout.
const RUNTIME_UPDATE_PROMPT_ROLLOUT = "force-20260703";

export interface RuntimeUpdatePrompt {
  key: string;
  runtimes: AgentRuntime[];
  targetVersion: string;
}

export function runtimeUpdatePrompts(
  runtimes: AgentRuntime[],
): RuntimeUpdatePrompt[] {
  const prompts = new Map<string, RuntimeUpdatePrompt>();
  for (const runtime of runtimes) {
    const targetVersion = runtimeTargetVersion(runtime);
    if (!targetVersion) continue;
    const key = [
      RUNTIME_UPDATE_PROMPT_ROLLOUT,
      runtimeUpdatePromptMachineKey(runtime),
      targetVersion,
    ].join(":");
    const existing = prompts.get(key);
    if (existing) {
      existing.runtimes.push(runtime);
    } else {
      prompts.set(key, { key, runtimes: [runtime], targetVersion });
    }
  }
  return Array.from(prompts.values()).sort((a, b) =>
    a.key.localeCompare(b.key),
  );
}

export function parseDismissedPromptKeys(value: string | null): Set<string> {
  if (!value) return new Set();
  try {
    const parsed = JSON.parse(value);
    if (Array.isArray(parsed)) {
      return new Set(
        parsed.filter((item): item is string => typeof item === "string"),
      );
    }
  } catch {
    // Older builds stored a single prompt key as the raw localStorage value.
  }
  return new Set([value]);
}

export function serializeDismissedPromptKeys(keys: Set<string>): string {
  return JSON.stringify(Array.from(keys).sort());
}

function runtimeUpdatePromptMachineKey(runtime: AgentRuntime): string {
  if (runtime.daemon_id?.trim()) {
    return `${runtime.runtime_mode}:daemon:${runtime.daemon_id.trim()}`;
  }
  const deviceName = runtimeDeviceName(runtime);
  if (deviceName) return `${runtime.runtime_mode}:device:${deviceName}`;
  return `${runtime.runtime_mode}:runtime:${runtime.id}`;
}

function runtimeDeviceName(runtime: AgentRuntime): string | null {
  const host = runtime.name.match(/^.+?\s+\(([^)]+)\)$/)?.[1]?.trim();
  if (host) return host;

  const raw = runtime.device_info?.trim();
  if (!raw) return null;
  return raw.split(" · ")[0]?.trim() || null;
}
