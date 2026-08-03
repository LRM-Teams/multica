import type { AgentRuntime } from "@multica/core/types";
import { knownProviderLabel } from "../../runtimes/components/provider-logo";
import {
  runtimeComputerLabel,
  splitRuntimeName,
} from "../../runtimes/components/runtime-machines";

/**
 * Frank / Parker 2026-08-03 — runtime picker primary label is the provider
 * brand (`Grok Build`, `Cursor`, …), not `Grok (ubuntu)` machine display names.
 */
export function runtimePickerBrandLabel(runtime: {
  provider: string;
}): string {
  return knownProviderLabel(runtime.provider) ?? runtime.provider;
}

/**
 * Host/computer subtitle only when the visible list has more than one runtime
 * of the same provider — otherwise the brand alone is enough.
 */
export function runtimePickerHostSubtitle(
  runtime: AgentRuntime,
  visible: readonly AgentRuntime[],
): string | null {
  const sameProvider = visible.filter((r) => r.provider === runtime.provider);
  if (sameProvider.length <= 1) return null;
  const computer = runtimeComputerLabel(runtime).trim();
  if (computer && computer !== "—") return computer;
  return splitRuntimeName(runtime.name).hostname?.trim() || null;
}
