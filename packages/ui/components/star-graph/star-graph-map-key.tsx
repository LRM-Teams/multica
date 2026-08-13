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

import { STAR_GRAPH_RELATIONS, type StarGraphRelationKey } from "./relations";
import {
  STAR_GRAPH_TIERS_LARGE_TO_SMALL,
  starGraphTierToken,
  type StarGraphTier,
} from "./tier";

export interface StarGraphMapKeyProps {
  /** Optional callback to re-open the on-boarding guide (help entry). */
  onHelp?: () => void;
  labels: {
    ariaLabel: string;
    mapKey: string;
    agentTier: string;
    help: string;
    tierDescriptions: Record<StarGraphTier, string>;
    relations: Record<
      StarGraphRelationKey,
      { label: string; description: string }
    >;
  };
  className?: string;
}

export function StarGraphMapKey({
  onHelp,
  labels,
  className,
}: StarGraphMapKeyProps) {
  const tierDots = STAR_GRAPH_TIERS_LARGE_TO_SMALL.map((tier) => ({
    tier,
    dot: tierDotClass(tier),
    label: tier === "s" ? labels.agentTier : starGraphTierToken(tier).label,
  }));

  return (
    <section
      data-testid="star-graph-map-key"
      aria-label={labels.ariaLabel}
      className={cn("sg-map-key", className)}
    >
      <span className="sr-only">{labels.mapKey}</span>
      {tierDots.map(({ tier, dot, label }) => {
        const desc = labels.tierDescriptions[tier];
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
          title={labels.relations[rel.key].description}
          aria-label={`${labels.relations[rel.key].label}: ${labels.relations[rel.key].description}`}
        >
          <i className={cn(rel.demoClass)} aria-hidden="true" />
          {labels.relations[rel.key].label}
        </button>
      ))}

      {onHelp && (
        <button
          type="button"
          data-testid="map-key-help"
          className="sg-item sg-menu-btn"
          onClick={onHelp}
          aria-label={labels.help}
        >
          ?
        </button>
      )}
    </section>
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
