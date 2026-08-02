"use client";

import { useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { ExplorationRail } from "./exploration-rail";
import { HumanBoundaryCard } from "./human-boundary-card";
import { SourceStrategyStrip } from "./source-strategy-strip";
import type {
  ExplorationDimension,
  HumanBoundaryModel,
  SourceStrategyModel,
} from "../lib/m2-visibility";

type Tab = "trail" | "sources" | "boundary";

export function VisibilityTabs({
  dimensions,
  strategy,
  boundary,
  sessionStatus,
  selectedFamily,
  selectedQuestionId,
  onSelectFamily,
  onSelectQuestion,
  className,
}: {
  dimensions: ExplorationDimension[];
  strategy: SourceStrategyModel;
  boundary: HumanBoundaryModel;
  sessionStatus?: string | null;
  selectedFamily?: string | null;
  selectedQuestionId?: string | null;
  onSelectFamily?: (family: string) => void;
  onSelectQuestion?: (nodeId: string) => void;
  className?: string;
}) {
  const { t } = useT("research");
  const [tab, setTab] = useState<Tab>("trail");

  return (
    <div
      data-testid="visibility-tabs"
      className={cn("flex flex-col border-b bg-background sm:hidden", className)}
    >
      <div className="flex border-b">
        {(
          [
            ["trail", t(($) => $.m2.tab_trail)],
            ["sources", t(($) => $.m2.tab_sources)],
            ["boundary", t(($) => $.m2.tab_boundary)],
          ] as const
        ).map(([id, label]) => (
          <button
            key={id}
            type="button"
            className={cn(
              "flex-1 px-2 py-2 text-center text-xs font-medium",
              tab === id
                ? "border-b-2 border-brand text-foreground"
                : "text-muted-foreground",
            )}
            onClick={() => setTab(id)}
          >
            {label}
          </button>
        ))}
      </div>
      <div className="max-h-[42vh] overflow-y-auto">
        {tab === "trail" ? (
          <ExplorationRail
            className="w-full border-r-0"
            dimensions={dimensions}
            sessionStatus={sessionStatus}
            selectedFamily={selectedFamily}
            selectedQuestionId={selectedQuestionId}
            onSelectFamily={onSelectFamily}
            onSelectQuestion={onSelectQuestion}
          />
        ) : null}
        {tab === "sources" ? (
          <SourceStrategyStrip
            model={strategy}
            sessionStatus={sessionStatus}
            className="border-b-0"
          />
        ) : null}
        {tab === "boundary" ? (
          <div className="p-3">
            <HumanBoundaryCard
              model={boundary}
              sessionStatus={sessionStatus}
            />
          </div>
        ) : null}
      </div>
    </div>
  );
}
