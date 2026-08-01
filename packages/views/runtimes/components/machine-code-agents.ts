import type { AgentRuntime } from "@multica/core/types";
import {
  KNOWN_PROVIDERS,
  knownProviderLabel,
  type KnownProvider,
} from "./provider-logo";
import { providerDocsUrl } from "./provider-docs";

export type MachineCodeAgentRow = {
  id: string;
  label: string;
  /**
   * Detected code-agent CLI version (from daemon `device_info`), not the
   * Multica daemon `current_version`. Null when unknown / not installed.
   */
  version: string | null;
  docsUrl: string | null;
};

export type MachineCodeAgentGroups = {
  installed: MachineCodeAgentRow[];
  notInstalled: MachineCodeAgentRow[];
};

function labelFor(provider: string): string {
  return knownProviderLabel(provider) ?? provider;
}

/**
 * `device_info` is assembled as `"hostname · <provider version half>"`
 * (e.g. `"host.local · 2.1.121 (Claude Code)"`, `"box · codex-cli 0.118.0"`).
 * Pull a semver-ish token from the second half — never use Multica
 * `current_version` here (that's the daemon, Parker 08-01).
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
  return match[0].replace(/^v/i, "");
}

/**
 * Split the known provider catalog into installed (present on this machine)
 * vs not installed. Installed providers that are not in the catalog still
 * appear in the installed group so custom / new providers aren't hidden.
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
      versionByProvider.set(
        provider,
        codeAgentVersionFromDeviceInfo(runtime.device_info),
      );
    }
  }

  const installedIds = Array.from(versionByProvider.keys()).sort((a, b) =>
    labelFor(a).localeCompare(labelFor(b)),
  );
  const installedSet = new Set(installedIds);

  const installed: MachineCodeAgentRow[] = installedIds.map((id) => ({
    id,
    label: labelFor(id),
    version: versionByProvider.get(id) ?? null,
    docsUrl: providerDocsUrl(id),
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
