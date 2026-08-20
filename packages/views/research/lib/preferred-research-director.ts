/** Match the workspace Ronaldo without pinning an environment UUID. */
const RONALDO_MARKERS = ["ronaldo", "luonaerduo", "luo-na-er-duo", "罗纳尔多"];

function normalizeDirectorLabel(value: string): string {
  return value.trim().toLowerCase().replace(/[\s_-]+/g, "");
}

export function preferredResearchDirectorId<
  T extends { id: string; name?: string | null; display_name?: string | null },
>(agents: readonly T[]): string {
  if (agents.length === 0) return "";
  const markers = RONALDO_MARKERS.map(normalizeDirectorLabel);
  const match = agents.find((agent) => {
    const haystack = normalizeDirectorLabel(`${agent.display_name ?? ""} ${agent.name ?? ""}`);
    return markers.some((marker) => haystack.includes(marker));
  });
  return match?.id ?? agents[0]?.id ?? "";
}
