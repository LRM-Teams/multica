import type { RuntimeTokenStats } from "@multica/core/types";

export function formatCompactRuntimeTokens(value?: number | null): string | null {
  if (value == null || !Number.isFinite(value) || value <= 0) return null;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 0 : 1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(value >= 100_000 ? 0 : 1)}k`;
  return String(Math.round(value));
}

export function formatRuntimeStatsMoney(value?: number | null): string | null {
  if (value == null || !Number.isFinite(value) || value <= 0) return null;
  return `$${value.toFixed(value >= 10 ? 2 : 3)}`;
}

export function runtimeTokenStatsLabel(stats?: RuntimeTokenStats | null): string | null {
  if (!stats) return null;
  const chunks: string[] = [];
  const input = formatCompactRuntimeTokens(stats.input_tokens);
  const output = formatCompactRuntimeTokens(stats.output_tokens);
  const cacheRead = formatCompactRuntimeTokens(stats.cache_read_tokens);
  if (input) chunks.push(`in ${input}`);
  if (output) chunks.push(`out ${output}`);
  // Prefer "cache N" over opaque "RN" — mobile truncates the badge and a lone
  // "R6" looked like a mysterious popup when users tapped the chip.
  if (cacheRead) chunks.push(`cache ${cacheRead}`);
  const cost = formatRuntimeStatsMoney(stats.cost_usd);
  if (cost) chunks.push(cost);
  if (stats.context_percent != null && Number.isFinite(stats.context_percent)) {
    const pct = stats.context_percent.toFixed(stats.context_percent >= 10 ? 1 : 2);
    const window = formatCompactRuntimeTokens(stats.context_window);
    chunks.push(window ? `${pct}%/${window}` : `${pct}%`);
  }
  if (stats.auto_compaction_enabled != null) chunks.push(stats.auto_compaction_enabled ? "auto" : "manual");
  return chunks.length ? chunks.join(" ") : null;
}
