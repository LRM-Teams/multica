"use client";

import { useId, type SVGProps } from "react";
import { cn } from "../../lib/utils";

type WarshipSpec = {
  hull: string;
  deck: string;
  accent: string;
  superstructure?: boolean;
  turret?: boolean;
  wings?: boolean;
};

function WarshipBadgeIcon({
  className,
  title,
  spec,
}: SVGProps<SVGSVGElement> & { title?: string; spec: WarshipSpec }) {
  const uid = useId().replace(/:/g, "");
  return (
    <svg viewBox="0 0 32 32" fill="none" aria-hidden={title ? undefined : true} className={className}>
      {title ? <title>{title}</title> : null}
      <defs>
        <linearGradient id={`${uid}-hull`} x1="8" y1="22" x2="24" y2="10">
          <stop stopColor={spec.hull} />
          <stop offset="1" stopColor={spec.deck} />
        </linearGradient>
        <linearGradient id={`${uid}-beam`} x1="16" y1="6" x2="16" y2="24">
          <stop stopColor="#fff" stopOpacity="0.55" />
          <stop offset="1" stopColor={spec.accent} stopOpacity="0" />
        </linearGradient>
        <radialGradient id={`${uid}-glow`} cx="50%" cy="40%" r="55%">
          <stop stopColor={spec.accent} stopOpacity="0.45" />
          <stop offset="1" stopColor={spec.accent} stopOpacity="0" />
        </radialGradient>
      </defs>
      <circle cx="16" cy="16" r="14" fill={`url(#${uid}-glow)`} opacity="0.55" />
      <path
        d="M4 18c3-1.5 7-2.2 12-2.2s9 .7 12 2.2l-1.2 3.2H5.2L4 18z"
        fill={`url(#${uid}-hull)`}
        stroke={spec.accent}
        strokeWidth="0.6"
        strokeOpacity="0.7"
      />
      <path d="M8 18.5h16" stroke={spec.accent} strokeWidth="0.5" strokeOpacity="0.35" />
      {spec.superstructure ? (
        <rect x="12.5" y="11" width="7" height="5.5" rx="1" fill={spec.deck} stroke={spec.accent} strokeWidth="0.5" />
      ) : null}
      {spec.turret ? (
        <>
          <circle cx="11" cy="15.5" r="1.6" fill={spec.accent} fillOpacity="0.85" />
          <circle cx="21" cy="15.5" r="1.6" fill={spec.accent} fillOpacity="0.85" />
        </>
      ) : null}
      {spec.wings ? (
        <path d="M6 17l3-4 1 4M26 17l-3-4-1 4" stroke={spec.accent} strokeWidth="1" strokeLinecap="round" opacity="0.75" />
      ) : null}
      <path d="M16 5v4" stroke={`url(#${uid}-beam)`} strokeWidth="1.2" strokeLinecap="round" />
      <ellipse cx="13" cy="12.5" rx="2" ry="1.1" fill="#fff" fillOpacity="0.28" />
    </svg>
  );
}

const CLASS_SPECS: Record<string, WarshipSpec> = {
  dreadnought: {
    hull: "#fcd34d",
    deck: "#b45309",
    accent: "#fbbf24",
    superstructure: true,
    turret: true,
    wings: true,
  },
  battleship: {
    hull: "#fdba74",
    deck: "#c2410c",
    accent: "#fb923c",
    superstructure: true,
    turret: true,
  },
  cruiser: {
    hull: "#7dd3fc",
    deck: "#0369a1",
    accent: "#38bdf8",
    superstructure: true,
    turret: true,
  },
  frigate: {
    hull: "#6ee7b7",
    deck: "#047857",
    accent: "#34d399",
    superstructure: true,
  },
  corvette: {
    hull: "#c4b5fd",
    deck: "#6d28d9",
    accent: "#a78bfa",
    wings: true,
  },
  reserve: {
    hull: "#cbd5e1",
    deck: "#64748b",
    accent: "#94a3b8",
  },
};

export function FleetClassIcon({
  classId,
  className,
  title,
}: {
  classId: string;
  className?: string;
  title?: string;
}) {
  const spec = CLASS_SPECS[classId] ?? CLASS_SPECS.reserve!;
  return (
    <WarshipBadgeIcon
      title={title}
      className={cn("size-full", className)}
      spec={spec}
    />
  );
}
