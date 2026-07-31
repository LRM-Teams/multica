"use client";

import { useId, type FC, type SVGProps } from "react";

export type HonorIconProps = SVGProps<SVGSVGElement> & { title?: string };

function HonorIconFrame({ title, className, children, ...props }: HonorIconProps & { children: React.ReactNode }) {
  return (
    <svg
      viewBox="0 0 32 32"
      fill="none"
      aria-hidden={title ? undefined : true}
      className={className}
      {...props}
    >
      {title ? <title>{title}</title> : null}
      {children}
    </svg>
  );
}

type PlanetSpec = {
  title?: string;
  className?: string;
  coreA: string;
  coreB: string;
  accent: string;
  ring?: boolean;
  ringTilt?: number;
  particles?: boolean;
};

function PlanetBadgeIcon({ title, className, coreA, coreB, accent, ring, ringTilt = -18, particles = true }: PlanetSpec) {
  const uid = useId().replace(/:/g, "");
  return (
    <HonorIconFrame title={title} className={className}>
      <defs>
        <radialGradient id={`${uid}-core`} cx="38%" cy="32%" r="68%">
          <stop stopColor={coreA} />
          <stop offset="1" stopColor={coreB} />
        </radialGradient>
        <linearGradient id={`${uid}-ring`} x1="4" y1="16" x2="28" y2="16">
          <stop stopColor={accent} stopOpacity="0.15" />
          <stop offset="0.5" stopColor={accent} stopOpacity="0.85" />
          <stop offset="1" stopColor={accent} stopOpacity="0.15" />
        </linearGradient>
        <radialGradient id={`${uid}-halo`} cx="50%" cy="50%" r="50%">
          <stop stopColor={accent} stopOpacity="0.35" />
          <stop offset="1" stopColor={accent} stopOpacity="0" />
        </radialGradient>
      </defs>
      <circle cx="16" cy="16" r="14" fill={`url(#${uid}-halo)`} opacity="0.5" />
      <circle cx="16" cy="16" r="10.5" stroke={accent} strokeWidth="0.6" strokeOpacity="0.35" />
      {ring ? (
        <ellipse
          cx="16"
          cy="16"
          rx="13"
          ry="4.2"
          stroke={`url(#${uid}-ring)`}
          strokeWidth="1.4"
          transform={`rotate(${ringTilt} 16 16)`}
        />
      ) : null}
      <circle cx="16" cy="16" r="8.2" fill={`url(#${uid}-core)`} stroke={accent} strokeWidth="0.5" strokeOpacity="0.6" />
      <ellipse cx="12.5" cy="12" rx="2.8" ry="1.6" fill="#fff" fillOpacity="0.28" />
      <path d="M8 20c2.5-1.2 5.2-1.8 8-1.8s5.5.6 8 1.8" stroke="#000" strokeOpacity="0.18" strokeWidth="0.8" />
      {particles ? (
        <>
          <circle cx="26" cy="7" r="0.9" fill={accent} fillOpacity="0.85" />
          <circle cx="6" cy="11" r="0.6" fill={accent} fillOpacity="0.55" />
          <circle cx="24" cy="24" r="0.5" fill="#fff" fillOpacity="0.65" />
        </>
      ) : null}
    </HonorIconFrame>
  );
}

export function GenesisNebulaIcon(props: HonorIconProps) {
  const uid = useId().replace(/:/g, "");
  return (
    <HonorIconFrame {...props}>
      <defs>
        <radialGradient id={`${uid}-n`} cx="50%" cy="45%" r="55%">
          <stop stopColor="#fde68a" />
          <stop offset="0.45" stopColor="#f59e0b" />
          <stop offset="1" stopColor="#7c3aed" />
        </radialGradient>
        <linearGradient id={`${uid}-beam`} x1="8" y1="8" x2="24" y2="24">
          <stop stopColor="#fff" stopOpacity="0.9" />
          <stop offset="1" stopColor="#fbbf24" stopOpacity="0" />
        </linearGradient>
      </defs>
      <circle cx="16" cy="16" r="13" fill={`url(#${uid}-n)`} opacity="0.35" />
      <path
        d="M16 4c2 4 2 8 0 12s-2 8 0 12M10 8c3 2 5 5 6 8M22 8c-3 2-5 5-6 8"
        stroke={`url(#${uid}-beam)`}
        strokeWidth="1.2"
        strokeLinecap="round"
        opacity="0.75"
      />
      <circle cx="16" cy="16" r="5.5" fill="#fde68a" stroke="#f59e0b" strokeWidth="0.8" />
      <circle cx="14" cy="14" r="1.8" fill="#fff" fillOpacity="0.45" />
      <circle cx="24" cy="9" r="1" fill="#c4b5fd" />
      <circle cx="7" cy="22" r="0.8" fill="#67e8f9" />
    </HonorIconFrame>
  );
}

export function StardustIcon(props: HonorIconProps) {
  const uid = useId().replace(/:/g, "");
  return (
    <HonorIconFrame {...props}>
      <defs>
        <radialGradient id={`${uid}-dust`} cx="50%" cy="50%" r="50%">
          <stop stopColor="#e2e8f0" />
          <stop offset="1" stopColor="#64748b" stopOpacity="0" />
        </radialGradient>
      </defs>
      <circle cx="16" cy="16" r="12" fill={`url(#${uid}-dust)`} opacity="0.55" />
      {[
        [16, 16, 1.4],
        [10, 11, 0.9],
        [22, 12, 0.8],
        [13, 22, 0.7],
        [21, 21, 0.65],
        [8, 17, 0.55],
      ].map(([cx, cy, r], i) => (
        <circle key={i} cx={cx} cy={cy} r={r} fill="#f8fafc" fillOpacity={0.5 + i * 0.08} />
      ))}
      <path d="M16 6v3M16 23v3M6 16h3M23 16h3" stroke="#94a3b8" strokeWidth="0.8" strokeLinecap="round" opacity="0.7" />
    </HonorIconFrame>
  );
}

export function MercuryIcon(props: HonorIconProps) {
  return <PlanetBadgeIcon {...props} coreA="#d4d4d8" coreB="#52525b" accent="#a1a1aa" />;
}
export function VenusIcon(props: HonorIconProps) {
  return <PlanetBadgeIcon {...props} coreA="#fde68a" coreB="#d97706" accent="#fbbf24" particles />;
}
export function EarthIcon(props: HonorIconProps) {
  return <PlanetBadgeIcon {...props} coreA="#38bdf8" coreB="#14532d" accent="#22d3ee" />;
}
export function MarsIcon(props: HonorIconProps) {
  return <PlanetBadgeIcon {...props} coreA="#fca5a5" coreB="#991b1b" accent="#f87171" />;
}
export function JupiterIcon(props: HonorIconProps) {
  return <PlanetBadgeIcon {...props} coreA="#fdba74" coreB="#9a3412" accent="#fb923c" ring={false} />;
}
export function SaturnIcon(props: HonorIconProps) {
  return <PlanetBadgeIcon {...props} coreA="#fde68a" coreB="#ca8a04" accent="#facc15" ring ringTilt={-22} />;
}
export function UranusIcon(props: HonorIconProps) {
  return <PlanetBadgeIcon {...props} coreA="#67e8f9" coreB="#0e7490" accent="#22d3ee" ring ringTilt={12} />;
}
export function NeptuneIcon(props: HonorIconProps) {
  return <PlanetBadgeIcon {...props} coreA="#6366f1" coreB="#1e1b4b" accent="#818cf8" />;
}
export function PlutoIcon(props: HonorIconProps) {
  return <PlanetBadgeIcon {...props} coreA="#d6d3d1" coreB="#44403c" accent="#a8a29e" particles ring ringTilt={-8} />;
}

function StellarBadgeIcon({
  title,
  className,
  inner,
  outer,
  flare,
}: HonorIconProps & { inner: string; outer: string; flare: string }) {
  const uid = useId().replace(/:/g, "");
  return (
    <HonorIconFrame title={title} className={className}>
      <defs>
        <radialGradient id={`${uid}-star`} cx="45%" cy="40%" r="65%">
          <stop stopColor={flare} />
          <stop offset="0.55" stopColor={inner} />
          <stop offset="1" stopColor={outer} />
        </radialGradient>
      </defs>
      <circle cx="16" cy="16" r="13" fill={outer} fillOpacity="0.25" />
      <path
        d="M16 3v4M16 25v4M3 16h4M25 16h4M7.5 7.5l2.8 2.8M21.7 21.7l2.8 2.8M7.5 24.5l2.8-2.8M21.7 10.3l2.8-2.8"
        stroke={flare}
        strokeWidth="1"
        strokeLinecap="round"
        opacity="0.65"
      />
      <circle cx="16" cy="16" r="7.5" fill={`url(#${uid}-star)`} stroke={flare} strokeWidth="0.7" />
      <circle cx="13.5" cy="13" r="2.2" fill="#fff" fillOpacity="0.35" />
    </HonorIconFrame>
  );
}

export function RedGiantIcon(props: HonorIconProps) {
  return <StellarBadgeIcon {...props} flare="#fecaca" inner="#ef4444" outer="#7f1d1d" />;
}
export function RedDwarfIcon(props: HonorIconProps) {
  return <StellarBadgeIcon {...props} flare="#fca5a5" inner="#dc2626" outer="#450a0a" />;
}
export function BlueGiantIcon(props: HonorIconProps) {
  return <StellarBadgeIcon {...props} flare="#bfdbfe" inner="#3b82f6" outer="#1e3a8a" />;
}
export function QuasarIcon(props: HonorIconProps) {
  const uid = useId().replace(/:/g, "");
  return (
    <HonorIconFrame {...props}>
      <defs>
        <radialGradient id={`${uid}-q`} cx="50%" cy="50%" r="50%">
          <stop stopColor="#fff" />
          <stop offset="0.35" stopColor="#fde68a" />
          <stop offset="1" stopColor="#7c3aed" />
        </radialGradient>
      </defs>
      <ellipse cx="16" cy="16" rx="14" ry="5" fill="#a855f7" fillOpacity="0.25" transform="rotate(-35 16 16)" />
      <ellipse cx="16" cy="16" rx="14" ry="5" fill="#22d3ee" fillOpacity="0.2" transform="rotate(35 16 16)" />
      <circle cx="16" cy="16" r="6" fill={`url(#${uid}-q)`} />
      <circle cx="16" cy="16" r="10" stroke="#c4b5fd" strokeWidth="0.8" strokeDasharray="2 2" opacity="0.7" />
      <path d="M4 16h24" stroke="#fde68a" strokeWidth="1.2" strokeLinecap="round" opacity="0.55" />
    </HonorIconFrame>
  );
}

export function ForgeRingIcon(props: HonorIconProps) {
  const uid = useId().replace(/:/g, "");
  return (
    <HonorIconFrame {...props}>
      <defs>
        <linearGradient id={`${uid}-forge`} x1="6" y1="6" x2="26" y2="26">
          <stop stopColor="#67e8f9" />
          <stop offset="1" stopColor="#0891b2" />
        </linearGradient>
      </defs>
      <circle cx="16" cy="16" r="11" stroke={`url(#${uid}-forge)`} strokeWidth="1.2" strokeOpacity="0.45" />
      <ellipse cx="16" cy="16" rx="12.5" ry="4" stroke="#22d3ee" strokeWidth="1.6" transform="rotate(-20 16 16)" />
      <path d="M11 20l5-9 5 9H11z" fill={`url(#${uid}-forge)`} stroke="#06b6d4" strokeWidth="0.7" />
      <circle cx="16" cy="14" r="2" fill="#ecfeff" fillOpacity="0.75" />
    </HonorIconFrame>
  );
}

export function TwinStarsIcon(props: HonorIconProps) {
  const uid = useId().replace(/:/g, "");
  return (
    <HonorIconFrame {...props}>
      <defs>
        <radialGradient id={`${uid}-a`} cx="40%" cy="35%" r="65%">
          <stop stopColor="#6ee7b7" />
          <stop offset="1" stopColor="#059669" />
        </radialGradient>
        <radialGradient id={`${uid}-b`} cx="40%" cy="35%" r="65%">
          <stop stopColor="#86efac" />
          <stop offset="1" stopColor="#16a34a" />
        </radialGradient>
      </defs>
      <path d="M3 18c3-3 7-4 13-4s10 1 13 4" stroke="#34d399" strokeWidth="1" strokeOpacity="0.45" />
      <circle cx="11" cy="13" r="5.2" fill={`url(#${uid}-a)`} stroke="#10b981" strokeWidth="0.6" />
      <circle cx="21" cy="13" r="5.2" fill={`url(#${uid}-b)`} stroke="#22c55e" strokeWidth="0.6" />
      <circle cx="9.5" cy="11.5" r="1.4" fill="#fff" fillOpacity="0.35" />
      <circle cx="19.5" cy="11.5" r="1.4" fill="#fff" fillOpacity="0.35" />
      <path d="M11 18c2 1.5 4.5 2 5 2s3-.5 5-2" stroke="#6ee7b7" strokeWidth="0.8" opacity="0.6" />
    </HonorIconFrame>
  );
}

type CatalogCosmicGlyph =
  | "moon"
  | "comet"
  | "asteroid"
  | "eclipse"
  | "pulse"
  | "sail"
  | "station"
  | "base"
  | "path"
  | "voyager"
  | "beacon"
  | "relay"
  | "archive"
  | "constellation"
  | "aurora"
  | "galaxy"
  | "wormhole"
  | "terrain"
  | "foundry"
  | "nexus"
  | "helix"
  | "prism"
  | "plasma"
  | "gate"
  | "singularity"
  | "crown"
  | "horizon"
  | "tree"
  | "infinity"
  | "photon"
  | "clock"
  | "diamond"
  | "supernova"
  | "blackhole";

type CatalogCosmicSpec = {
  glyph: CatalogCosmicGlyph;
  core: string;
  edge: string;
  accent: string;
};

const catalogCosmicSpecs: Record<string, CatalogCosmicSpec> = {
  moon: { glyph: "moon", core: "#e2e8f0", edge: "#475569", accent: "#a5f3fc" },
  comet: { glyph: "comet", core: "#fef3c7", edge: "#c2410c", accent: "#67e8f9" },
  asteroid: { glyph: "asteroid", core: "#d6d3d1", edge: "#44403c", accent: "#fbbf24" },
  eclipse: { glyph: "eclipse", core: "#c4b5fd", edge: "#111827", accent: "#fb7185" },
  pulsar: { glyph: "pulse", core: "#f0abfc", edge: "#581c87", accent: "#67e8f9" },
  solar_sail: { glyph: "sail", core: "#fde68a", edge: "#9a3412", accent: "#f8fafc" },
  orbital_station: { glyph: "station", core: "#93c5fd", edge: "#1e3a8a", accent: "#5eead4" },
  lunar_base: { glyph: "base", core: "#e7e5e4", edge: "#57534e", accent: "#c4b5fd" },
  pathfinder: { glyph: "path", core: "#6ee7b7", edge: "#065f46", accent: "#fef08a" },
  voyager: { glyph: "voyager", core: "#7dd3fc", edge: "#075985", accent: "#f0abfc" },
  beacon: { glyph: "beacon", core: "#fef08a", edge: "#a16207", accent: "#67e8f9" },
  relay: { glyph: "relay", core: "#86efac", edge: "#166534", accent: "#93c5fd" },
  archive: { glyph: "archive", core: "#fdba74", edge: "#9a3412", accent: "#c4b5fd" },
  constellation: { glyph: "constellation", core: "#bfdbfe", edge: "#312e81", accent: "#fde68a" },
  aurora: { glyph: "aurora", core: "#5eead4", edge: "#115e59", accent: "#f0abfc" },
  galaxy: { glyph: "galaxy", core: "#c4b5fd", edge: "#4c1d95", accent: "#67e8f9" },
  wormhole: { glyph: "wormhole", core: "#f0abfc", edge: "#701a75", accent: "#93c5fd" },
  terraformer: { glyph: "terrain", core: "#86efac", edge: "#14532d", accent: "#fdba74" },
  foundry: { glyph: "foundry", core: "#fb923c", edge: "#7c2d12", accent: "#67e8f9" },
  nexus: { glyph: "nexus", core: "#a5b4fc", edge: "#3730a3", accent: "#5eead4" },
  helix: { glyph: "helix", core: "#67e8f9", edge: "#155e75", accent: "#f0abfc" },
  prism_core: { glyph: "prism", core: "#f8fafc", edge: "#6d28d9", accent: "#22d3ee" },
  plasma_orb: { glyph: "plasma", core: "#f9a8d4", edge: "#9d174d", accent: "#fde68a" },
  quantum_gate: { glyph: "gate", core: "#a5b4fc", edge: "#312e81", accent: "#67e8f9" },
  singularity: { glyph: "singularity", core: "#f8fafc", edge: "#020617", accent: "#c084fc" },
  celestial_crown: { glyph: "crown", core: "#fde68a", edge: "#92400e", accent: "#f8fafc" },
  event_horizon: { glyph: "horizon", core: "#818cf8", edge: "#020617", accent: "#fb7185" },
  cosmic_tree: { glyph: "tree", core: "#6ee7b7", edge: "#064e3b", accent: "#c4b5fd" },
  infinity: { glyph: "infinity", core: "#f8fafc", edge: "#4c1d95", accent: "#fde68a" },
  photon_ring: { glyph: "photon", core: "#67e8f9", edge: "#164e63", accent: "#fef08a" },
  chronosphere: { glyph: "clock", core: "#c4b5fd", edge: "#312e81", accent: "#5eead4" },
  diamond_star: { glyph: "diamond", core: "#f8fafc", edge: "#0369a1", accent: "#c4b5fd" },
  supernova: { glyph: "supernova", core: "#fef3c7", edge: "#be123c", accent: "#f8fafc" },
  black_hole: { glyph: "blackhole", core: "#a78bfa", edge: "#020617", accent: "#fbbf24" },
};

function CatalogCosmicBadgeIcon({
  spec,
  title,
  className,
  ...props
}: HonorIconProps & { spec: CatalogCosmicSpec }) {
  const uid = useId().replace(/:/g, "");
  return (
    <HonorIconFrame title={title} className={className} {...props}>
      <defs>
        <radialGradient id={`${uid}-catalog-core`} cx="38%" cy="32%" r="70%">
          <stop stopColor={spec.core} />
          <stop offset="1" stopColor={spec.edge} />
        </radialGradient>
        <radialGradient id={`${uid}-catalog-halo`}>
          <stop stopColor={spec.accent} stopOpacity="0.42" />
          <stop offset="1" stopColor={spec.accent} stopOpacity="0" />
        </radialGradient>
      </defs>
      <circle cx="16" cy="16" r="14" fill={`url(#${uid}-catalog-halo)`} opacity="0.6" />
      <circle
        cx="16"
        cy="16"
        r="11"
        fill={`url(#${uid}-catalog-core)`}
        fillOpacity="0.32"
        stroke={spec.accent}
        strokeOpacity="0.45"
        strokeWidth="0.7"
      />
      {catalogCosmicGlyph(spec.glyph, spec, uid)}
    </HonorIconFrame>
  );
}

function catalogCosmicGlyph(
  glyph: CatalogCosmicGlyph,
  spec: CatalogCosmicSpec,
  uid: string,
) {
  const strokeProps = {
    stroke: spec.accent,
    strokeWidth: 1.35,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
  };
  switch (glyph) {
    case "moon":
      return <path d="M20.8 7.6a9 9 0 1 0 3.6 14.8A8 8 0 0 1 20.8 7.6Z" fill={spec.core} {...strokeProps} />;
    case "comet":
      return <><path d="M5 23 18 10M7 27 21 12M4 18 16 8" {...strokeProps} opacity=".65" /><circle cx="21" cy="10" r="5" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /></>;
    case "asteroid":
      return <path d="m9 9 7-3 7 4 3 7-5 8-8 1-7-6 1-7 2-4Z" fill={`url(#${uid}-catalog-core)`} {...strokeProps} />;
    case "eclipse":
      return <><circle cx="14" cy="16" r="8" fill={spec.core} opacity=".9" /><circle cx="19" cy="14" r="8" fill={spec.edge} /><path d="M23 7a10 10 0 0 1 1 17" {...strokeProps} /></>;
    case "pulse":
      return <><circle cx="16" cy="16" r="4" fill={spec.core} /><path d="M4 16h7l2-5 4 10 3-5h8M16 3v5M16 24v5" {...strokeProps} /></>;
    case "sail":
      return <><path d="m8 23 8-17 8 17-8-3-8 3Z" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="M16 6v20" {...strokeProps} /></>;
    case "station":
      return <><rect x="11" y="11" width="10" height="10" rx="2" fill={spec.core} {...strokeProps} /><path d="M3 13h8M21 13h8M6 9v8M26 9v8M16 3v8M16 21v8" {...strokeProps} /></>;
    case "base":
      return <><path d="M6 23h20M9 23v-8l7-6 7 6v8M13 23v-5h6v5" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="M7 10h6" {...strokeProps} /></>;
    case "path":
      return <><path d="M6 25c3-10 6-3 10-12s8-4 10-9" {...strokeProps} /><path d="m21 4 5 0-1 5" {...strokeProps} /><circle cx="7" cy="24" r="2" fill={spec.core} /></>;
    case "voyager":
      return <><path d="m6 20 18-12-7 18-3-8-8 2Z" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="m14 18-6 8" {...strokeProps} /></>;
    case "beacon":
      return <><path d="M12 24h8l-2-13h-4l-2 13Z" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="M8 10a11 11 0 0 1 16 0M5 7a15 15 0 0 1 22 0" {...strokeProps} opacity=".7" /></>;
    case "relay":
      return <><circle cx="8" cy="16" r="3" fill={spec.core} /><circle cx="24" cy="9" r="3" fill={spec.core} /><circle cx="24" cy="23" r="3" fill={spec.core} /><path d="m11 15 10-5M11 17l10 5M24 12v8" {...strokeProps} /></>;
    case "archive":
      return <><rect x="8" y="7" width="16" height="19" rx="2" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="M11 12h10M11 16h10M11 20h7" {...strokeProps} /></>;
    case "constellation":
      return <><path d="m6 22 6-12 7 5 7-8M12 10l2 14 5-9 7 7" {...strokeProps} opacity=".75" />{[[6,22],[12,10],[14,24],[19,15],[26,7],[26,22]].map(([x,y]) => <circle key={`${x}-${y}`} cx={x} cy={y} r="1.6" fill={spec.core} />)}</>;
    case "aurora":
      return <><path d="M5 23C8 6 11 26 16 9c4 17 7-3 11 14" {...strokeProps} /><path d="M6 18C10 8 12 23 16 12c4 11 7-2 10 6" stroke={spec.core} strokeWidth="2" opacity=".55" /></>;
    case "galaxy":
      return <><path d="M5 18c5 8 20 7 22-2 2-8-12-13-19-6-6 6 4 13 11 9 5-3 1-8-3-7" fill="none" {...strokeProps} /><circle cx="16" cy="16" r="2.5" fill={spec.core} /></>;
    case "wormhole":
      return <><ellipse cx="16" cy="16" rx="12" ry="6" {...strokeProps} /><ellipse cx="16" cy="16" rx="8" ry="4" {...strokeProps} opacity=".75" /><ellipse cx="16" cy="16" rx="4" ry="2" fill={spec.core} /></>;
    case "terrain":
      return <><circle cx="16" cy="16" r="10" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="M8 18c3-3 4 1 7-2s5 2 9-2M10 11c2 2 4-1 6 1" {...strokeProps} /></>;
    case "foundry":
      return <><path d="M7 25V12l5 4v-5l5 5V9l8 5v11H7Z" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="M11 25v-5h4v5M20 19h2" {...strokeProps} /></>;
    case "nexus":
      return <><circle cx="16" cy="16" r="4" fill={spec.core} /><path d="M16 4v8M16 20v8M4 16h8M20 16h8M8 8l5 5M24 8l-5 5M8 24l5-5M24 24l-5-5" {...strokeProps} /></>;
    case "helix":
      return <><path d="M10 5c12 6 0 16 12 22M22 5c-12 6 0 16-12 22M12 9h8M11 15h10M11 21h10" {...strokeProps} /></>;
    case "prism":
      return <><path d="m16 5 9 19H7L16 5Z" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="m16 5 0 19M7 24l9-7 9 7" {...strokeProps} /></>;
    case "plasma":
      return <><circle cx="16" cy="16" r="8" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="M16 6c4 4-4 5 0 9s-3 5 0 11M8 16c4-3 5 3 8 0s5 3 8 0" {...strokeProps} opacity=".8" /></>;
    case "gate":
      return <><path d="M7 26V13a9 9 0 0 1 18 0v13M11 26V14a5 5 0 0 1 10 0v12" {...strokeProps} /><path d="M16 8v18" stroke={spec.core} strokeWidth="2" /></>;
    case "singularity":
      return <><circle cx="16" cy="16" r="4" fill={spec.edge} stroke={spec.core} /><path d="M4 16c6-12 18-12 24 0-6 12-18 12-24 0Z" fill="none" {...strokeProps} /><path d="M8 7c16 2 16 16 0 18M24 7C8 9 8 23 24 25" {...strokeProps} opacity=".55" /></>;
    case "crown":
      return <><path d="m6 10 6 6 4-9 4 9 6-6-2 15H8L6 10Z" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="M9 21h14" {...strokeProps} /></>;
    case "horizon":
      return <><circle cx="16" cy="16" r="6" fill={spec.edge} /><ellipse cx="16" cy="16" rx="14" ry="5" {...strokeProps} /><path d="M4 16h24" stroke={spec.core} strokeWidth="1.8" /></>;
    case "tree":
      return <><path d="M16 26V14M16 18 9 12M16 17l7-7M16 21l-6 3M16 21l7 3" {...strokeProps} /><circle cx="9" cy="11" r="4" fill={spec.core} opacity=".7" /><circle cx="23" cy="9" r="4" fill={spec.core} opacity=".7" /><circle cx="16" cy="8" r="5" fill={spec.core} opacity=".55" /></>;
    case "infinity":
      return <path d="M4 16c4-8 8-8 12 0s8 8 12 0c-4-8-8-8-12 0S8 24 4 16Z" fill="none" {...strokeProps} strokeWidth="2.4" />;
    case "photon":
      return <><circle cx="16" cy="16" r="4" fill={spec.core} /><ellipse cx="16" cy="16" rx="13" ry="5" {...strokeProps} transform="rotate(30 16 16)" /><ellipse cx="16" cy="16" rx="13" ry="5" {...strokeProps} transform="rotate(-30 16 16)" /></>;
    case "clock":
      return <><circle cx="16" cy="16" r="10" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="M16 10v7l5 3M16 3v3M16 26v3M3 16h3M26 16h3" {...strokeProps} /></>;
    case "diamond":
      return <><path d="m16 4 9 12-9 12L7 16 16 4Z" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><path d="m7 16 9-4 9 4-9 5-9-5Z" {...strokeProps} opacity=".7" /></>;
    case "supernova":
      return <><path d="m16 2 3 10 9-5-6 9 8 4-10 1 2 9-6-7-6 7 2-9-10-1 8-4-6-9 9 5 3-10Z" fill={`url(#${uid}-catalog-core)`} {...strokeProps} /><circle cx="16" cy="16" r="4" fill={spec.core} /></>;
    case "blackhole":
      return <><circle cx="16" cy="16" r="5" fill={spec.edge} /><ellipse cx="16" cy="16" rx="14" ry="5" {...strokeProps} transform="rotate(-18 16 16)" /><ellipse cx="16" cy="16" rx="10" ry="3" stroke={spec.core} strokeWidth="1.6" transform="rotate(-18 16 16)" /></>;
  }
}

function catalogCosmicIcon(spec: CatalogCosmicSpec): FC<HonorIconProps> {
  return function CatalogIcon(props: HonorIconProps) {
    return <CatalogCosmicBadgeIcon {...props} spec={spec} />;
  };
}

export const HONOR_BADGE_ICONS: Record<string, FC<HonorIconProps>> = {
  genesis_nebula: GenesisNebulaIcon,
  stardust: StardustIcon,
  mercury: MercuryIcon,
  venus: VenusIcon,
  earth: EarthIcon,
  mars: MarsIcon,
  jupiter: JupiterIcon,
  saturn: SaturnIcon,
  uranus: UranusIcon,
  neptune: NeptuneIcon,
  pluto: PlutoIcon,
  red_giant: RedGiantIcon,
  red_dwarf: RedDwarfIcon,
  blue_giant: BlueGiantIcon,
  quasar: QuasarIcon,
  forge_ring: ForgeRingIcon,
  twin_stars: TwinStarsIcon,
  moon: catalogCosmicIcon(catalogCosmicSpecs.moon!),
  comet: catalogCosmicIcon(catalogCosmicSpecs.comet!),
  asteroid: catalogCosmicIcon(catalogCosmicSpecs.asteroid!),
  eclipse: catalogCosmicIcon(catalogCosmicSpecs.eclipse!),
  pulsar: catalogCosmicIcon(catalogCosmicSpecs.pulsar!),
  solar_sail: catalogCosmicIcon(catalogCosmicSpecs.solar_sail!),
  orbital_station: catalogCosmicIcon(catalogCosmicSpecs.orbital_station!),
  lunar_base: catalogCosmicIcon(catalogCosmicSpecs.lunar_base!),
  pathfinder: catalogCosmicIcon(catalogCosmicSpecs.pathfinder!),
  voyager: catalogCosmicIcon(catalogCosmicSpecs.voyager!),
  beacon: catalogCosmicIcon(catalogCosmicSpecs.beacon!),
  relay: catalogCosmicIcon(catalogCosmicSpecs.relay!),
  archive: catalogCosmicIcon(catalogCosmicSpecs.archive!),
  constellation: catalogCosmicIcon(catalogCosmicSpecs.constellation!),
  aurora: catalogCosmicIcon(catalogCosmicSpecs.aurora!),
  galaxy: catalogCosmicIcon(catalogCosmicSpecs.galaxy!),
  wormhole: catalogCosmicIcon(catalogCosmicSpecs.wormhole!),
  terraformer: catalogCosmicIcon(catalogCosmicSpecs.terraformer!),
  foundry: catalogCosmicIcon(catalogCosmicSpecs.foundry!),
  nexus: catalogCosmicIcon(catalogCosmicSpecs.nexus!),
  helix: catalogCosmicIcon(catalogCosmicSpecs.helix!),
  prism_core: catalogCosmicIcon(catalogCosmicSpecs.prism_core!),
  plasma_orb: catalogCosmicIcon(catalogCosmicSpecs.plasma_orb!),
  quantum_gate: catalogCosmicIcon(catalogCosmicSpecs.quantum_gate!),
  singularity: catalogCosmicIcon(catalogCosmicSpecs.singularity!),
  celestial_crown: catalogCosmicIcon(catalogCosmicSpecs.celestial_crown!),
  event_horizon: catalogCosmicIcon(catalogCosmicSpecs.event_horizon!),
  cosmic_tree: catalogCosmicIcon(catalogCosmicSpecs.cosmic_tree!),
  infinity: catalogCosmicIcon(catalogCosmicSpecs.infinity!),
  photon_ring: catalogCosmicIcon(catalogCosmicSpecs.photon_ring!),
  chronosphere: catalogCosmicIcon(catalogCosmicSpecs.chronosphere!),
  diamond_star: catalogCosmicIcon(catalogCosmicSpecs.diamond_star!),
  supernova: catalogCosmicIcon(catalogCosmicSpecs.supernova!),
  black_hole: catalogCosmicIcon(catalogCosmicSpecs.black_hole!),
  // Legacy svg_key aliases (pre-cosmic catalog).
  founding: GenesisNebulaIcon,
  veteran: RedGiantIcon,
  builder: ForgeRingIcon,
  collaborator: TwinStarsIcon,
};
