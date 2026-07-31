export function honorBadgeTone(svgKey: string): "gold" | "cyan" | "violet" | "amber" | "emerald" | "neutral" {
  if (svgKey.includes("genesis") || svgKey === "founding") return "gold";
  if (svgKey.includes("quasar") || svgKey.includes("blue") || svgKey.includes("neptune")) return "cyan";
  if (svgKey.includes("red") || svgKey.includes("mars")) return "amber";
  if (svgKey.includes("jupiter") || svgKey.includes("saturn") || svgKey.includes("venus")) return "gold";
  if (svgKey.includes("earth") || svgKey.includes("twin") || svgKey.includes("forge")) return "emerald";
  if (svgKey.includes("pluto") || svgKey.includes("mercury") || svgKey.includes("stardust")) return "neutral";
  return "violet";
}
