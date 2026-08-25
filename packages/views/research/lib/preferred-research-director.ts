/** Match the workspace Ronaldo without pinning an environment UUID. */
const RONALDO_MARKERS = ["ronaldo", "luonaerduo", "luo-na-er-duo", "罗纳尔多"];

function normalizeDirectorLabel(value: string): string {
  return value.trim().toLowerCase().replace(/[\s_-]+/g, "");
}

const NORMALIZED_RONALDO_MARKERS = RONALDO_MARKERS.map(normalizeDirectorLabel);

export function preferredResearchDirectorId<
  T extends { id: string; name?: string | null; display_name?: string | null },
>(agents: readonly T[]): string {
  const match = agents.find((agent) => {
    const haystack = normalizeDirectorLabel(`${agent.display_name ?? ""} ${agent.name ?? ""}`);
    return NORMALIZED_RONALDO_MARKERS.some((marker) => haystack.includes(marker));
  });
  return match?.id ?? "";
}
