import type { AgentRuntime } from "@multica/core/types";
import { KNOWN_PROVIDERS, type KnownProvider } from "./provider-logo";
import { providerDocsUrl } from "./provider-docs";

export type MachineCodeAgentRow = {
  id: string;
  label: string;
  /**
   * Detected code-agent CLI version. Prefer `metadata.version` (daemon
   * registration field for the CA). Never use Multica `current_version` /
   * `runtimeCurrentVersion` — that reads `metadata.cli_version` (daemon).
   */
  version: string | null;
  docsUrl: string | null;
};

export type MachineCodeAgentGroups = {
  installed: MachineCodeAgentRow[];
  notInstalled: MachineCodeAgentRow[];
};

function normalizeVersion(raw: string): string {
  return raw.trim().replace(/^v/i, "");
}

/**
 * Fallback when `metadata.version` is missing: `device_info` is assembled as
 * `"hostname · <version half>"` (e.g. `"host.local · 2.1.121 (Claude Code)"`).
 */
export function codeAgentVersionFromDeviceInfo(
  deviceInfo: string | null | undefined,
): string | null {
  const raw = deviceInfo?.trim();
  if (!raw) return null;
  const sep = raw.indexOf(" · ");
  if (sep < 0) return null;
  const half = raw.slice(sep + 3).trim();
  if (!half) return null;
  const match = half.match(/v?\d+(?:\.\d+)+(?:-[0-9A-Za-z.-]+)?/);
  if (!match?.[0]) return half;
  return normalizeVersion(match[0]);
}

/** CA CLI version for a registered runtime — not the Multica daemon version. */
export function codeAgentVersion(runtime: AgentRuntime): string | null {
  const meta = runtime.metadata?.version;
  if (typeof meta === "string" && meta.trim()) {
    return normalizeVersion(meta);
  }
  return codeAgentVersionFromDeviceInfo(runtime.device_info);
}

/**
 * Split the catalog into installed vs not installed on this machine.
 * Only catalog entries appear in either group (Frank/Iris: six providers).
 * Other detected providers are ignored for this section.
 */
export function partitionMachineCodeAgents(
  runtimes: AgentRuntime[],
  known: readonly KnownProvider[] = KNOWN_PROVIDERS,
): MachineCodeAgentGroups {
  const versionByProvider = new Map<string, string | null>();
  for (const runtime of runtimes) {
    const provider = runtime.provider?.trim();
    if (!provider) continue;
    if (!versionByProvider.has(provider)) {
      versionByProvider.set(provider, codeAgentVersion(runtime));
    }
  }

  const installedSet = new Set(versionByProvider.keys());

  const installed: MachineCodeAgentRow[] = known
    .filter((entry) => installedSet.has(entry.id))
    .map((entry) => ({
      id: entry.id,
      label: entry.label,
      version: versionByProvider.get(entry.id) ?? null,
      docsUrl: providerDocsUrl(entry.id),
    }));

  const notInstalled: MachineCodeAgentRow[] = known
    .filter((entry) => !installedSet.has(entry.id))
    .map((entry) => ({
      id: entry.id,
      label: entry.label,
      version: null,
      docsUrl: providerDocsUrl(entry.id),
    }));

  return { installed, notInstalled };
}
