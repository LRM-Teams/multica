/** D5 display lens ids — client-only display mode, never mutates canonical graph. */
export type ResearchD5Lens = "relations" | "confidence" | "agent" | "lineage";

export const RESEARCH_D5_LENSES: readonly ResearchD5Lens[] = [
  "agent",
  "lineage",
];

export function isResearchD5Lens(value: string | null | undefined): value is ResearchD5Lens {
  return RESEARCH_D5_LENSES.includes(value as ResearchD5Lens);
}

export const DEFAULT_RESEARCH_D5_LENS: ResearchD5Lens = "agent";
