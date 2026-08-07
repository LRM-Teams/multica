"use client";

/**
 * StarGraphMapKey — compact D5 Map Key (LRM-1496).
 *
 * A stateless presentation component in `@multica/ui`. It renders the compact
 * bottom tool-area legend: the five tier dots (XXL/XL/L/M/S) and the four
 * relation line-semantics (分解 / 支持 / 挑战 / 新方向), plus an optional help
 * button that re-opens the on-boarding guide.
 *
 * Every dot / line has a plain-language `title`/`aria-label` explaining what
 * it represents and what clicking does, so a first-time user understands the
 * visual system without digging into engineering terms.
 */

import { cn } from "@multica/ui/lib/utils";

import {
  STAR_GRAPH_TIERS_LARGE_TO_SMALL,
  starGraphTierToken,
  type StarGraphTier,
} from "./tier";

export interface StarGraphRelationToken {
  /** Stable key. */
  key: string;
  /** Short label shown in the Map Key. */
  label: string;
  /** Plain-language hover/focus explanation. */
  description: string;
  /** CSS class for the line demo (support/challenge/newdir). */
  demoClass: string;
}

export const STAR_GRAPH_RELATIONS: readonly StarGraphRelationToken[] = [
  {
    key: "decompose",
    label: "分解",
    description: "实线：一个目标/方向分解出多个子方向或子任务。",
    demoClass: "sg-line-demo",
  },
  {
    key: "support",
    label: "支持",
    description: "绿色实线：一个成果支持另一个成果或结论。",
    demoClass: "sg-line-demo sg-support",
  },
  {
    key: "challenge",
    label: "挑战",
    description: "橙色虚线：一个成果挑战或反驳另一个结论。",
    demoClass: "sg-line-demo sg-challenge",
  },
  {
    key: "newdir",
    label: "新方向",
    description: "紫色虚线：与既有成果无关的全新探索方向。",
    demoClass: "sg-line-demo sg-newdir",
  },
];

export interface StarGraphMapKeyProps {
  /** Optional callback to re-open the on-boarding guide (help entry). */
  onHelp?: () => void;
  /** Override tier label (e.g. "Agent" instead of "S"). */
  tierSOverridesLabel?: string;
  className?: string;
}

export function StarGraphMapKey({
  onHelp,
  tierSOverridesLabel,
  className,
}: StarGraphMapKeyProps) {
  const tierDots = STAR_GRAPH_TIERS_LARGE_TO_SMALL.map((tier) => ({
    tier,
    dot: tierDotClass(tier),
    label: tier === "s" ? tierSOverridesLabel ?? "Agent" : starGraphTierToken(tier).label,
  }));

  return (
    <div
      data-testid="star-graph-map-key"
      role="group"
      aria-label="星图图例"
      className={cn("sg-map-key", className)}
    >
      <span className="sr-only">Map Key</span>
      {tierDots.map(({ tier, dot, label }) => {
        const desc = starGraphTierToken(tier).description;
        return (
          <button
            key={tier}
            type="button"
            data-tier={tier}
            data-testid={`map-key-tier-${tier}`}
            className="sg-item"
            title={label}
            aria-label={`${label}：${desc}`}
          >
            <i className={cn("sg-dot", dot)} aria-hidden="true" />
            {label}
          </button>
        );
      })}

      <i className="block h-6 w-px bg-muted-foreground/20" aria-hidden="true" />

      {STAR_GRAPH_RELATIONS.map((rel) => (
        <button
          key={rel.key}
          type="button"
          data-relation={rel.key}
          data-testid={`map-key-relation-${rel.key}`}
          className="sg-item"
          title={rel.description}
          aria-label={`${rel.label}：${rel.description}`}
        >
          <i className={cn(rel.demoClass)} aria-hidden="true" />
          {rel.label}
        </button>
      ))}

      {onHelp && (
        <button
          type="button"
          data-testid="map-key-help"
          className="sg-item sg-menu-btn"
          onClick={onHelp}
          aria-label="重新打开星图引导"
        >
          ?
        </button>
      )}
    </div>
  );
}

function tierDotClass(tier: StarGraphTier): string {
  switch (tier) {
    case "xxl":
      return "sg-lv1";
    case "xl":
      return "sg-lv2";
    case "l":
      return "sg-lv3";
    case "m":
      return "sg-lv4";
    case "s":
      return "sg-lv5";
  }
}
