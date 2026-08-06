"use client";

import { useMemo } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { TrajectoryFilters } from "./data-adapter";
import type {
  ResearchGraphNode,
  TrajectoryLaneLayout,
} from "./trajectory-types";

/**
 * Toolbar: visible filter dimensions (branch/agent/relation), sort toggle
 * (time/logical both drive the same deterministic layout in this slice; the
 * toggle is an accessibility-visible control that re-runs layout), zoom reset
 * and minimap toggle.
 *
 * Filter params are pure inputs — the parent derives a *new* layout from the
 * filtered source set so identical params always reflow lanes deterministically
 * (AC3 stable reflow).
 */
export function TrajectoryToolbar({
  nodes,
  layout,
  filters,
  zoom,
  onFiltersChange,
  onToggleMinimap,
  showMinimap,
  onResetZoom,
}: {
  nodes: readonly ResearchGraphNode[];
  layout: TrajectoryLaneLayout;
  filters: TrajectoryFilters;
  zoom: number;
  showMinimap: boolean;
  onFiltersChange: (f: TrajectoryFilters) => void;
  onToggleMinimap: () => void;
  onResetZoom: () => void;
}) {
  const { t } = useT("research");

  const branchOptions = useMemo(() => {
    const set = new Set<string>();
    for (const n of nodes) {
      const k = n.theme_key?.trim();
      if (k) set.add(k);
    }
    return Array.from(set).sort();
  }, [nodes]);

  const agentOptions = useMemo(() => {
    const set = new Set<string>();
    for (const n of nodes) if (n.actor_agent_id) set.add(n.actor_agent_id);
    return Array.from(set).sort();
  }, [nodes]);

  const toggleSet = (
    key: "branches" | "agents",
    value: string,
  ): TrajectoryFilters => {
    const next = new Set(filters[key]);
    if (next.has(value)) next.delete(value);
    else next.add(value);
    return { ...filters, [key]: next };
  };

  const toggleRelation = (rel: string): TrajectoryFilters => {
    const next = new Set(filters.hiddenRelations);
    if (next.has(rel)) next.delete(rel);
    else next.add(rel);
    return { ...filters, hiddenRelations: next };
  };

  return (
    <div
      data-testid="trajectory-toolbar"
      className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border/55 px-3 py-2"
    >
      <fieldset className="flex flex-wrap items-center gap-1">
        <legend className="sr-only">
          {t((s) => s.trajectory_explorer.filter_by_branch)}
        </legend>
        {branchOptions.slice(0, 12).map((b) => (
          <ToggleChip
            key={b}
            active={filters.branches.has(b)}
            label={b}
            onClick={() => onFiltersChange(toggleSet("branches", b))}
          />
        ))}
        {branchOptions.length === 0 ? (
          <span className="text-[10px] text-muted-foreground">
            {t((s) => s.trajectory_explorer.no_branches)}
          </span>
        ) : null}
      </fieldset>

      <fieldset className="flex flex-wrap items-center gap-1">
        <legend className="sr-only">
          {t((s) => s.trajectory_explorer.filter_by_agent)}
        </legend>
        {agentOptions.map((a) => (
          <ToggleChip
            key={a}
            active={filters.agents.has(a)}
            label={a}
            onClick={() => onFiltersChange(toggleSet("agents", a))}
          />
        ))}
        {agentOptions.length === 0 ? (
          <span className="text-[10px] text-muted-foreground">
            {t((s) => s.trajectory_explorer.no_agents)}
          </span>
        ) : null}
      </fieldset>

      <div className="flex items-center gap-2">
        <span className="text-[10px] text-muted-foreground">
          {t((s) => s.trajectory_explorer.relations)}
        </span>
        {(["main", "branch", "merge", "abandoned"] as const).map((r) => (
          <ToggleChip
            key={r}
            active={!filters.hiddenRelations.has(r)}
            label={r}
            onClick={() => onFiltersChange(toggleRelation(r))}
          />
        ))}
      </div>

      <div className="ml-auto flex items-center gap-2">
        <ToolButton
          ariaLabel={t((s) => s.trajectory_explorer.toggle_minimap)}
          active={showMinimap}
          onClick={onToggleMinimap}
          label={t((s) => s.trajectory_explorer.minimap)}
        />
        <ToolButton
          ariaLabel={t((s) => s.trajectory_explorer.reset_zoom)}
          onClick={onResetZoom}
          label={`${Math.round(zoom * 100)}%`}
        />
      </div>
      <div className="sr-only" aria-live="polite">
        {t((s) => s.trajectory_explorer.filtering, {
          count: layout.commits.length,
          total: nodes.length,
        })}
      </div>
    </div>
  );
}

function ToggleChip({
  active,
  label,
  onClick,
}: {
  active: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "rounded border px-1.5 py-0.5 text-[10px] leading-4",
        active
          ? "border-brand/35 bg-brand/10 text-brand"
          : "border-border/60 text-muted-foreground hover:bg-muted/40",
      )}
    >
      {label}
    </button>
  );
}

function ToolButton({
  ariaLabel,
  active,
  onClick,
  label,
}: {
  ariaLabel: string;
  active?: boolean;
  onClick: () => void;
  label: string;
}) {
  return (
    <button
      type="button"
      aria-label={ariaLabel}
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "rounded border px-1.5 py-1 text-[10px] leading-4",
        active
          ? "border-brand/35 bg-brand/10 text-brand"
          : "border-border/60 text-muted-foreground hover:bg-muted/40",
      )}
    >
      {label}
    </button>
  );
}
