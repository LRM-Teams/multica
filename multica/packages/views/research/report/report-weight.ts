export type WeightTier = "hi" | "mid" | "lo";

export function weightTier(weight: number): WeightTier {
  if (weight >= 0.8) return "hi";
  if (weight >= 0.6) return "mid";
  return "lo";
}
