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
  // Legacy svg_key aliases (pre-cosmic catalog).
  founding: GenesisNebulaIcon,
  veteran: RedGiantIcon,
  builder: ForgeRingIcon,
  collaborator: TwinStarsIcon,
};
