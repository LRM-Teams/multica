"use client";

/**
 * StarGraphNode — the shareable D5 tiered circular node surface (LRM-1496).
 *
 * A stateless, presentation-only React component in `@multica/ui`. It renders
 * one circular node for any of the five tiers (XXL / XL / L / M / S) from the
 * real data its parent supplies. It contains NO research domain logic and
 * never imports `@multica/core`:
 *   - it never maps node kinds → tiers (that lives in views/star-graph);
 *   - it never reads ids / status keys / status strings;
 *   - every datum arrives as an explicit prop (title, shortLabel, agentBadge,
 *     roundLabel, metrics, state) so Web and Desktop share one component.
 *
 * Accessibility (D5 AC): the node is a real focusable button with a readable
 * `aria-label` built from its own labels; state is communicated through text,
 * glyph and line/badge cues, not colour alone. Text clamps into the circle and
 * never spills the boundary.
 */

import { cn } from "@multica/ui/lib/utils";
import { ChevronRight, LoaderCircle, RotateCcw } from "lucide-react";

import { starGraphTierToken, type StarGraphTier } from "./tier";
import { starGraphStateToken, type StarGraphNodeState } from "./state";

export interface StarGraphNodeMetrics {
  /** Document / source count. Omit when absent. */
  documentCount?: number;
  /** Confidence percent 0..100. Omit when absent. */
  confidence?: number;
  /** Number of conclusions / findings. Omit when absent. */
  conclusionCount?: number;
  /** Round label (e.g. "R2"). */
  round?: string;
}

export interface StarGraphNodeProps {
  /** Stable renderer id used by the canvas roving-focus controller. */
  nodeId?: string;
  /** Tier — determines size + ring + glow surface. */
  tier: StarGraphTier;
  /** Visual state. Defaults to `default`. */
  state?: StarGraphNodeState;
  /** Node title (primary text shown inside the circle). */
  title: string;
  /** Short secondary line (round / role / effort). */
  subLabel?: string;
  /** Short tier header (e.g. "MASTER SYNTHESIS", "STABLE RESULT"). */
  headerLabel?: string;
  /** Canonical role marker; intentionally independent from the visual tier. */
  semanticRole?: "goal";
  /** S-tier agent badge (e.g. "A1"). Only rendered for tier `s`. */
  agentBadge?: string;
  /** S-tier body treatment. `point` keeps text in the accessible name only. */
  sTierPresentation?: "label" | "point";
  /** Live metrics for XL/XXL/L. */
  metrics?: StarGraphNodeMetrics;
  /** Fully formatted localized metric strings supplied by the product layer. */
  metricText?: {
    documentCount?: string;
    confidence?: string;
    conclusionCount?: string;
    documentBadge?: string;
  };
  /** Override the computed accessible name (D5 keyboard/SR contract). */
  accessibleName?: string;
  /** Canvas uses roving tabindex so a large graph contributes one tab stop. */
  tabIndex?: number;
  /** Explicitly busy (spinner/pulse). */
  busy?: boolean;
  /** Independent selection state; canonical lifecycle state may have higher visual priority. */
  selected?: boolean;
  /** Server-backed disclosure state; omitted for ordinary graph nodes. */
  expanded?: boolean;
  /** Request-level failure; distinct from canonical node lifecycle. */
  invalid?: boolean;
  /** Grid position on the canvas (left/top in % or px). Optional. */
  style?: React.CSSProperties;
  onOpen?: () => void;
  className?: string;
}

/**
 * Render a tiered circular node. Unknown tiers / states degrade to a safe
 * default (never throw), mirroring the GenericNode degradation rule.
 */
export function StarGraphNode({
  nodeId,
  tier,
  state = "default",
  title,
  subLabel,
  headerLabel,
  semanticRole,
  agentBadge,
  sTierPresentation = "label",
  metrics,
  metricText,
  busy,
  selected,
  expanded,
  invalid,
  accessibleName,
  tabIndex,
  style,
  onOpen,
  className,
}: StarGraphNodeProps) {
  const token = starGraphTierToken(tier);
  const stateToken = starGraphStateToken(state);

  const readable =
    accessibleName?.trim() ||
    [headerLabel, title, stateToken.ariaLabel, subLabel, agentBadge]
      .filter(Boolean)
      .join("，") ||
    title;

  const size = `${token.sizePx}px`;

  const documentCount = metrics?.documentCount;
  const hasDocuments = documentCount != null && documentCount > 0;
  const showMetrics =
    token.tier !== "s" &&
    (hasDocuments ||
      metrics?.confidence != null ||
      metrics?.conclusionCount != null);
  const showDocumentBadge =
    (token.tier === "xxl" || token.tier === "xl" || token.tier === "l") &&
    hasDocuments;
  const metricSummary = metricsSummaryText(metrics, metricText, showDocumentBadge);
  const rendersAsPoint = token.tier === "s" && sTierPresentation === "point";

  return (
    <button
      type="button"
      data-node-id={nodeId}
      tabIndex={tabIndex}
      aria-label={readable}
      aria-busy={busy || undefined}
      aria-pressed={selected || undefined}
      aria-expanded={expanded}
      aria-invalid={invalid || undefined}
      onClick={onOpen}
      data-tier={tier}
      data-semantic-role={semanticRole}
      data-state={state}
      data-presentation={rendersAsPoint ? "point" : "label"}
      data-testid="star-graph-node"
      className={cn(
        "sg-node",
        `sg-tier-${tier}`,
        token.ringCount > 0 ? `sg-ring-${token.ringCount}` : undefined,
        token.glow > 0 ? `sg-glow-${token.glow}` : undefined,
        busy || state === "run" ? "sg-state-run" : undefined,
        rendersAsPoint ? "sg-s-point" : undefined,
        selected ? "sg-is-selected" : undefined,
        className,
      )}
      style={{
        width: size,
        height: size,
        ...style,
      }}
    >
      <span className="sg-core">
        {token.tier === "s" && !rendersAsPoint ? (
          <SNodeContent
            shortLabel={title}
            agentBadge={agentBadge}
          />
        ) : token.tier !== "s" ? (
          <>
            {headerLabel && (
              <span
                data-testid="star-graph-header"
                className="block w-full truncate text-[0.55rem] font-black tracking-[0.14em] text-muted-foreground"
              >
                {headerLabel}
              </span>
            )}
            <span
              data-testid="star-graph-title"
              className="sg-title block w-full font-semibold"
            >
              {title}
            </span>
            {subLabel && (
              <span
                data-testid="star-graph-sub"
                className="sg-sub sg-summary block w-full"
              >
                {subLabel}
              </span>
            )}
            {showMetrics && metricSummary && (
              <span
                data-testid="star-graph-metrics"
                className="sg-sub sg-metrics mt-0.5 block w-full"
              >
                {metricSummary}
              </span>
            )}
          </>
        ) : null}
      </span>
      {showDocumentBadge && (
        <span data-testid="star-graph-document-badge" className="sg-document-badge">
          {metricText?.documentBadge ?? `DOC · ${documentCount}`}
        </span>
      )}
      {!rendersAsPoint && stateToken.glyph !== "none" && (
        <span
          data-testid="star-graph-glyph"
          data-glyph={stateToken.glyph}
          className={cn("sg-glyph", "absolute -top-1 -right-1")}
          aria-hidden="true"
        >
          {glyphChar(stateToken.glyph)}
        </span>
      )}
      {expanded !== undefined ? (
        <span
          data-testid="star-graph-disclosure"
          data-disclosure-state={
            invalid ? "failed" : busy ? "loading" : expanded ? "expanded" : "collapsed"
          }
          className="sg-disclosure"
          aria-hidden="true"
        >
          {invalid ? (
            <RotateCcw className="size-3" />
          ) : busy ? (
            <LoaderCircle className="size-3 animate-spin" />
          ) : (
            <ChevronRight
              className={cn("size-3 transition-transform", expanded && "rotate-90")}
            />
          )}
        </span>
      ) : null}
    </button>
  );
}

function SNodeContent({
  shortLabel,
  agentBadge,
}: {
  shortLabel: string;
  agentBadge?: string;
}) {
  return (
    <>
      <span
        data-testid="star-graph-s-label"
        className="sg-title block w-full truncate text-[0.6rem] font-bold"
      >
        {shortLabel}
      </span>
      {agentBadge && (
        <span
          data-testid="star-graph-agent-badge"
          className="mt-0.5 inline-grid min-w-[1.4rem] place-items-center rounded-full border px-1 text-[0.5rem] font-black"
        >
          {agentBadge}
        </span>
      )}
    </>
  );
}

function metricsSummaryText(
  metrics: StarGraphNodeMetrics | undefined,
  localized: StarGraphNodeProps["metricText"],
  omitDocumentCount = false,
): string {
  if (!metrics) return "";
  const parts: string[] = [];
  if (metrics.round) parts.push(`R${metrics.round}`);
  if (!omitDocumentCount && metrics.documentCount != null && metrics.documentCount > 0) {
    parts.push(localized?.documentCount ?? `DOC · ${metrics.documentCount}`);
  }
  if (metrics.confidence != null) {
    parts.push(localized?.confidence ?? `${metrics.confidence}%`);
  }
  if (metrics.conclusionCount != null) {
    parts.push(localized?.conclusionCount ?? `Σ · ${metrics.conclusionCount}`);
  }
  return parts.join(" · ");
}

function glyphChar(glyph: string): string {
  switch (glyph) {
    case "pulse":
      return "◌";
    case "check":
      return "✓";
    case "spinner":
      return "↻";
    case "exclaim":
      return "!";
    case "ban":
      return "⊗";
    case "restart":
      return "↻";
    default:
      return "";
  }
}

export type { StarGraphTier };
