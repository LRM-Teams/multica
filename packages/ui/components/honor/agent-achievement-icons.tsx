"use client";

import { useId, type FC, type SVGProps } from "react";

export type AgentArmorIconProps = SVGProps<SVGSVGElement> & { title?: string };

type ArmorPalette = {
  plate: string;
  shadow: string;
  accent: string;
  glow: string;
  highlight?: string;
};

function ArmorIconFrame({
  uid,
  title,
  className,
  palette,
  children,
}: AgentArmorIconProps & { uid: string; palette: ArmorPalette; children: React.ReactNode }) {
  return (
    <svg
      viewBox="0 0 32 32"
      fill="none"
      aria-hidden={title ? undefined : true}
      className={className}
    >
      {title ? <title>{title}</title> : null}
      <defs>
        <linearGradient id={`${uid}-plate`} x1="8" y1="6" x2="24" y2="26">
          <stop stopColor={palette.plate} />
          <stop offset="1" stopColor={palette.shadow} />
        </linearGradient>
        <linearGradient id={`${uid}-edge`} x1="16" y1="4" x2="16" y2="28">
          <stop stopColor={palette.highlight ?? palette.accent} stopOpacity="0.85" />
          <stop offset="1" stopColor={palette.accent} stopOpacity="0.15" />
        </linearGradient>
        <radialGradient id={`${uid}-glow`} cx="50%" cy="45%" r="55%">
          <stop stopColor={palette.glow} stopOpacity="0.55" />
          <stop offset="1" stopColor={palette.glow} stopOpacity="0" />
        </radialGradient>
      </defs>
      <circle cx="16" cy="16" r="14" fill={`url(#${uid}-glow)`} opacity="0.65" />
      {children}
    </svg>
  );
}

const PALETTES = {
  launch: { plate: "#cbd5e1", shadow: "#475569", accent: "#38bdf8", glow: "#0ea5e9" },
  crew: { plate: "#94a3b8", shadow: "#334155", accent: "#67e8f9", glow: "#22d3ee" },
  veteran: { plate: "#fdba74", shadow: "#9a3412", accent: "#fbbf24", glow: "#f97316" },
  centurion: { plate: "#fde68a", shadow: "#92400e", accent: "#fcd34d", glow: "#f59e0b" },
  streak: { plate: "#fca5a5", shadow: "#991b1b", accent: "#fb7185", glow: "#ef4444" },
  orbit: { plate: "#c4b5fd", shadow: "#5b21b6", accent: "#a78bfa", glow: "#8b5cf6" },
  memory: { plate: "#7dd3fc", shadow: "#0369a1", accent: "#22d3ee", glow: "#06b6d4" },
  archive: { plate: "#93c5fd", shadow: "#1e40af", accent: "#60a5fa", glow: "#3b82f6" },
  constellation: { plate: "#ddd6fe", shadow: "#4c1d95", accent: "#c4b5fd", glow: "#a855f7" },
  seed: { plate: "#86efac", shadow: "#166534", accent: "#4ade80", glow: "#22c55e" },
  engine: { plate: "#f0abfc", shadow: "#86198f", accent: "#e879f9", glow: "#d946ef" },
  explorer: { plate: "#67e8f9", shadow: "#0e7490", accent: "#2dd4bf", glow: "#14b8a6" },
  phoenix: { plate: "#fdba74", shadow: "#c2410c", accent: "#fb923c", glow: "#f97316", highlight: "#fef08a" },
  corvette: { plate: "#a5b4fc", shadow: "#3730a3", accent: "#818cf8", glow: "#6366f1" },
  cruiser: { plate: "#fcd34d", shadow: "#b45309", accent: "#fbbf24", glow: "#f59e0b" },
  dreadnought: { plate: "#fde68a", shadow: "#78350f", accent: "#fbbf24", glow: "#eab308", highlight: "#fff7ed" },
  locked: { plate: "#64748b", shadow: "#1e293b", accent: "#94a3b8", glow: "#475569" },
} as const satisfies Record<string, ArmorPalette>;

function strokeEdge(color: string, width = 0.55) {
  return { stroke: color, strokeWidth: width, strokeOpacity: 0.75 };
}

export function AgentArmorFirstLaunchIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.launch;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M11 22c0-4 2.2-7.5 5-9.5s6.5-1.8 9-0.5c-1.2 2.8-3.4 5.2-6.2 6.8S13.5 21 11 22Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent)}
      />
      <path d="M16 8v5M14 10l2-2 2 2" stroke={p.accent} strokeWidth="1.2" strokeLinecap="round" />
      <path
        d="M13 24c1.2-2.5 3-3.8 5-3.8s3.8 1.3 5 3.8"
        stroke={p.glow}
        strokeWidth="1.4"
        strokeLinecap="round"
        opacity="0.9"
      />
      <ellipse cx="16" cy="24.5" rx="4" ry="1.2" fill={p.glow} fillOpacity="0.35" />
    </ArmorIconFrame>
  );
}

export function AgentArmorProvenCrewIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.crew;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M9 11h14l-1.5 13H10.5L9 11Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent)}
      />
      <path d="M12 14h8M12 17.5h8M12 21h8" stroke={p.accent} strokeWidth="0.8" strokeOpacity="0.55" />
      <path d="M16 7l3 4H13l3-4Z" fill={p.shadow} stroke={p.accent} strokeWidth="0.5" />
      <circle cx="12" cy="10" r="1" fill={p.glow} />
      <circle cx="20" cy="10" r="1" fill={p.glow} />
    </ArmorIconFrame>
  );
}

export function AgentArmorVeteranCoreIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.veteran;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M8.5 12c2-1.5 5-2 7.5-2s5.5.5 7.5 2l-1 12.5H9.5L8.5 12Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent)}
      />
      <circle cx="16" cy="16.5" r="4.2" fill={p.shadow} stroke={p.accent} strokeWidth="0.7" />
      <circle cx="16" cy="16.5" r="2.2" fill={p.glow} fillOpacity="0.85" />
      <path d="M16 12.5v8M12.5 16.5h7" stroke="#fff" strokeWidth="0.6" strokeOpacity="0.35" />
    </ArmorIconFrame>
  );
}

export function AgentArmorCenturionIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.centurion;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M10 18c0-5 2.7-8.5 6-8.5s6 3.5 6 8.5v4.5H10V18Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent)}
      />
      <path d="M16 6l2.5 4.5H13.5L16 6Z" fill={p.glow} stroke={p.accent} strokeWidth="0.5" />
      <rect x="12" y="15" width="8" height="3.5" rx="1" fill="#0f172a" fillOpacity="0.55" stroke={p.accent} strokeWidth="0.5" />
      <path d="M11 22.5h10" stroke={p.accent} strokeWidth="0.8" strokeLinecap="round" />
    </ArmorIconFrame>
  );
}

export function AgentArmorStreak5Icon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.streak;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M12 8c3 1 5 3.5 5.5 6.5S19 20 21 24H11c1.5-3.5 2.2-7 1.8-10.5S12.8 10.5 12 8Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent)}
      />
      {[0, 1, 2, 3, 4].map((i) => (
        <path
          key={i}
          d={`M${14 + i * 1.1} ${22 - i * 1.8}l1.2-2.4`}
          stroke={p.glow}
          strokeWidth="0.9"
          strokeLinecap="round"
          opacity={0.45 + i * 0.12}
        />
      ))}
      <ellipse cx="14" cy="11" rx="2" ry="1.2" fill="#fff" fillOpacity="0.25" />
    </ArmorIconFrame>
  );
}

export function AgentArmorStreak20Icon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.orbit;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <ellipse
        cx="16"
        cy="16"
        rx="11"
        ry="4.5"
        stroke={p.accent}
        strokeWidth="1.1"
        transform="rotate(-22 16 16)"
        opacity="0.85"
      />
      <ellipse
        cx="16"
        cy="16"
        rx="9"
        ry="3.2"
        stroke={p.glow}
        strokeWidth="0.6"
        transform="rotate(-22 16 16)"
        opacity="0.45"
      />
      <path
        d="M11 14c1.5-3 4-4.5 5-4.5s3.5 1.5 5 4.5l-1 10H12l-1-10Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent)}
      />
      <circle cx="23" cy="11" r="1.3" fill={p.glow} />
    </ArmorIconFrame>
  );
}

export function AgentArmorMemorySparkIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.memory;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M8 14c3-2 6-2 8 0s5 2 8 0l-2 9.5H10L8 14Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent)}
      />
      <path d="M16 9l3.5 4.5-3.5 2.5-3.5-2.5L16 9Z" fill={p.glow} stroke={p.accent} strokeWidth="0.55" />
      <path d="M16 9v3.5" stroke="#fff" strokeWidth="0.5" strokeOpacity="0.45" />
    </ArmorIconFrame>
  );
}

export function AgentArmorMemoryArchiveIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.archive;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <rect x="9" y="9" width="14" height="15" rx="1.5" fill={`url(#${uid}-plate)`} {...strokeEdge(p.accent)} />
      {[0, 1, 2].map((row) =>
        [0, 1].map((col) => (
          <rect
            key={`${row}-${col}`}
            x={11 + col * 5.5}
            y={11.5 + row * 4}
            width="4"
            height="2.6"
            rx="0.4"
            fill={row === 0 && col === 0 ? p.glow : p.shadow}
            fillOpacity={row === 0 && col === 0 ? 0.9 : 0.65}
            stroke={p.accent}
            strokeWidth="0.35"
            strokeOpacity="0.45"
          />
        )),
      )}
    </ArmorIconFrame>
  );
}

const MEMORY_CONSTELLATION_STARS: readonly (readonly [number, number])[] = [
  [10, 12],
  [16, 9],
  [22, 13],
  [19, 19],
  [13, 21],
  [11, 17],
];

export function AgentArmorMemoryConstellationIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.constellation;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M7 12c3-1 6-1 9 0s6 1 9 0l-3 12H10L7 12Z"
        fill={p.shadow}
        fillOpacity="0.75"
        stroke={p.accent}
        strokeWidth="0.55"
      />
      <path
        d="M10 12 16 9 22 13 19 19 13 21 11 17 10 12"
        stroke={p.accent}
        strokeWidth="0.7"
        strokeOpacity="0.65"
      />
      {MEMORY_CONSTELLATION_STARS.map(([x, y]) => (
        <circle key={`${x}-${y}`} cx={x} cy={y} r="1.1" fill={p.glow} />
      ))}
    </ArmorIconFrame>
  );
}

export function AgentArmorEvolutionSeedIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.seed;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M13 22c0-5 1.5-8.5 3-10.5s3.5-2.5 5-2.5 2.5 0.5 3 2.5 3 5.5 3 10.5H13Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent)}
      />
      <path d="M16 8c0 2-1 3.5-2 5M16 8c0 2 1 3.5 2 5" stroke={p.glow} strokeWidth="1" strokeLinecap="round" />
      <circle cx="16" cy="17" r="2.3" fill={p.glow} fillOpacity="0.75" />
    </ArmorIconFrame>
  );
}

export function AgentArmorEvolutionEngineIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.engine;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <rect x="11" y="11" width="10" height="12" rx="2" fill={`url(#${uid}-plate)`} {...strokeEdge(p.accent)} />
      <circle cx="13.5" cy="17" r="2.3" fill={p.shadow} stroke={p.glow} strokeWidth="0.6" />
      <circle cx="18.5" cy="17" r="2.3" fill={p.shadow} stroke={p.glow} strokeWidth="0.6" />
      <path d="M9 15l2 2M23 15l-2 2M9 19l2-2M23 19l-2-2" stroke={p.accent} strokeWidth="0.8" strokeLinecap="round" />
    </ArmorIconFrame>
  );
}

export function AgentArmorDeepSpaceIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.explorer;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M9.5 14c2.5-2 5-2.5 6.5-2.5s4 .5 6.5 2.5l-1.5 9.5H11L9.5 14Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent)}
      />
      <path
        d="M11.5 16.5h9"
        stroke={p.glow}
        strokeWidth="2.2"
        strokeLinecap="round"
        opacity="0.35"
      />
      <circle cx="13" cy="16.5" r="0.7" fill="#fff" />
      <circle cx="16" cy="16.5" r="0.7" fill={p.glow} />
      <circle cx="19" cy="16.5" r="0.7" fill="#fff" />
      <path d="M14 10l2-2 2 2" stroke={p.accent} strokeWidth="0.8" strokeLinecap="round" />
    </ArmorIconFrame>
  );
}

export function AgentArmorPhoenixIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.phoenix;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M16 22V12M10 20c2-4 4-6 6-8M22 20c-2-4-4-6-6-8"
        stroke={p.glow}
        strokeWidth="1.3"
        strokeLinecap="round"
      />
      <path
        d="M7 21c3-5 6-7 9-9s6 4 9 9"
        fill={`url(#${uid}-plate)`}
        fillOpacity="0.55"
        {...strokeEdge(p.accent)}
      />
      <path d="M16 8l1.5 3H14.5L16 8Z" fill={p.highlight ?? p.glow} />
    </ArmorIconFrame>
  );
}

export function AgentArmorCorvetteIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.corvette;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M10 12h12l-1 11H11l-1-11Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent)}
      />
      <path
        d="M8 18c2.5-1 5-1.5 8-1.5s5.5.5 8 1.5"
        fill={p.shadow}
        stroke={p.accent}
        strokeWidth="0.55"
      />
      <path d="M16 7v3" stroke={p.glow} strokeWidth="1.1" strokeLinecap="round" />
      <circle cx="13" cy="16" r="0.9" fill={p.glow} />
      <circle cx="19" cy="16" r="0.9" fill={p.glow} />
    </ArmorIconFrame>
  );
}

export function AgentArmorCruiserIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.cruiser;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M8.5 13c3-1.5 6.5-2 7.5-2s4.5.5 7.5 2l-1.2 11H9.7L8.5 13Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent)}
      />
      <rect x="13" y="8" width="6" height="4.5" rx="0.8" fill={p.shadow} stroke={p.accent} strokeWidth="0.5" />
      <path d="M12 17h8M12 20h8" stroke={p.accent} strokeWidth="0.65" strokeOpacity="0.55" />
      <circle cx="16" cy="15.5" r="1.4" fill={p.glow} />
    </ArmorIconFrame>
  );
}

export function AgentArmorDreadnoughtIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.dreadnought;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M9 13c2.8-2 6-2.8 7-2.8s4.2.8 7 2.8l-1.5 11.5H10.5L9 13Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent, 0.65)}
      />
      <path d="M11 9h10l-1.5 3H12.5L11 9Z" fill={p.glow} stroke={p.accent} strokeWidth="0.55" />
      <path d="M7 14l3 1M25 14l-3 1" stroke={p.highlight ?? p.glow} strokeWidth="0.9" strokeLinecap="round" />
      <circle cx="16" cy="17" r="2.6" fill={p.shadow} stroke={p.glow} strokeWidth="0.65" />
      <path d="M16 14.5v5M13.8 16.8h4.4" stroke="#fff" strokeWidth="0.55" strokeOpacity="0.35" />
    </ArmorIconFrame>
  );
}

export function AgentArmorLockedIcon(props: AgentArmorIconProps) {
  const uid = useId().replace(/:/g, "");
  const p = PALETTES.locked;
  return (
    <ArmorIconFrame {...props} uid={uid} palette={p}>
      <path
        d="M10 17c0-4 2.7-6.5 6-6.5s6 2.5 6 6.5v5.5H10V17Z"
        fill={`url(#${uid}-plate)`}
        {...strokeEdge(p.accent, 0.45)}
        opacity="0.75"
      />
      <rect x="13" y="18" width="6" height="4.5" rx="1" fill="#0f172a" fillOpacity="0.55" stroke={p.accent} strokeWidth="0.45" />
      <path d="M14.5 18v-1.8a1.5 1.5 0 0 1 3 0V18" stroke={p.accent} strokeWidth="0.7" />
      <text x="16" y="15.5" textAnchor="middle" fill={p.accent} fontSize="5.5" fontWeight="700" opacity="0.85">
        ?
      </text>
    </ArmorIconFrame>
  );
}

/** Agent honor achievement catalog — cosmic armor series. */
export const AGENT_ACHIEVEMENT_ICONS: Record<string, FC<AgentArmorIconProps>> = {
  agent_armor_first_launch: AgentArmorFirstLaunchIcon,
  agent_armor_proven_crew: AgentArmorProvenCrewIcon,
  agent_armor_veteran_core: AgentArmorVeteranCoreIcon,
  agent_armor_centurion: AgentArmorCenturionIcon,
  agent_armor_streak_5: AgentArmorStreak5Icon,
  agent_armor_streak_20: AgentArmorStreak20Icon,
  agent_armor_memory_spark: AgentArmorMemorySparkIcon,
  agent_armor_memory_archive: AgentArmorMemoryArchiveIcon,
  agent_armor_memory_constellation: AgentArmorMemoryConstellationIcon,
  agent_armor_evolution_seed: AgentArmorEvolutionSeedIcon,
  agent_armor_evolution_engine: AgentArmorEvolutionEngineIcon,
  agent_armor_deep_space: AgentArmorDeepSpaceIcon,
  agent_armor_phoenix: AgentArmorPhoenixIcon,
  agent_armor_corvette: AgentArmorCorvetteIcon,
  agent_armor_cruiser: AgentArmorCruiserIcon,
  agent_armor_dreadnought: AgentArmorDreadnoughtIcon,
  agent_armor_locked: AgentArmorLockedIcon,
};

export const AGENT_ACHIEVEMENT_PREVIEW: Array<{
  id: string;
  title: string;
  category: string;
  svgKey: keyof typeof AGENT_ACHIEVEMENT_ICONS;
  secret?: boolean;
}> = [
  { id: "first_launch", title: "First Launch", category: "delivery", svgKey: "agent_armor_first_launch" },
  { id: "proven_crew", title: "Proven Crew", category: "delivery", svgKey: "agent_armor_proven_crew" },
  { id: "veteran_core", title: "Veteran Core", category: "delivery", svgKey: "agent_armor_veteran_core" },
  { id: "centurion", title: "Centurion", category: "delivery", svgKey: "agent_armor_centurion" },
  { id: "streak_5", title: "Clean Burn", category: "reliability", svgKey: "agent_armor_streak_5" },
  { id: "streak_20", title: "Unbroken Orbit", category: "reliability", svgKey: "agent_armor_streak_20", secret: true },
  { id: "memory_spark", title: "Memory Spark", category: "growth", svgKey: "agent_armor_memory_spark" },
  { id: "memory_archive", title: "Living Archive", category: "growth", svgKey: "agent_armor_memory_archive" },
  { id: "memory_constellation", title: "Memory Constellation", category: "growth", svgKey: "agent_armor_memory_constellation", secret: true },
  { id: "evolution_seed", title: "Evolution Seed", category: "evolution", svgKey: "agent_armor_evolution_seed" },
  { id: "evolution_engine", title: "Evolution Engine", category: "evolution", svgKey: "agent_armor_evolution_engine", secret: true },
  { id: "deep_space_explorer", title: "Deep Space Explorer", category: "mastery", svgKey: "agent_armor_deep_space" },
  { id: "phoenix_protocol", title: "Phoenix Protocol", category: "reliability", svgKey: "agent_armor_phoenix", secret: true },
  { id: "corvette_command", title: "Corvette Command", category: "fleet", svgKey: "agent_armor_corvette" },
  { id: "cruiser_command", title: "Cruiser Command", category: "fleet", svgKey: "agent_armor_cruiser" },
  { id: "dreadnought_command", title: "Dreadnought Command", category: "fleet", svgKey: "agent_armor_dreadnought", secret: true },
];
