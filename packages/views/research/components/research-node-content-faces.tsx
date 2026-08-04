"use client";

import type { ResearchGraphNode } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import {
  CONTENT_FACE_KEYS,
  type ContentFaceDensity,
  type ContentFaceKey,
  resolveContentFaceValues,
} from "../lib/research-node-content-faces";

function faceLabelKey(key: ContentFaceKey): "goal" | "operation_approach" | "research_approach" | "result" {
  return key;
}

export function ResearchNodeContentFaces({
  node,
  density,
  className,
}: {
  node: ResearchGraphNode;
  density: ContentFaceDensity;
  className?: string;
}) {
  const { t } = useT("research");
  const copy = {
    missing: t(($) => $.content_faces.missing),
    resultPending:
      density === "detail"
        ? t(($) => $.content_faces.result_pending_detail)
        : t(($) => $.content_faces.result_pending),
    resultFailed:
      density === "detail"
        ? t(($) => $.content_faces.result_failed_detail)
        : t(($) => $.content_faces.result_failed),
  };
  const values = resolveContentFaceValues(node, density, copy);

  if (density === "detail") {
    return (
      <div
        className={cn("space-y-4", className)}
        data-testid="research-node-content-faces-detail"
      >
        {CONTENT_FACE_KEYS.map((key) => (
          <section key={key} data-content-face={key}>
            <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.content_faces[faceLabelKey(key)])}
            </h3>
            <p className="whitespace-pre-wrap text-sm leading-relaxed text-foreground">
              {values[key]}
            </p>
          </section>
        ))}
      </div>
    );
  }

  // surface: desktop 2×2; narrow stack via `layout="stack"` class override from parent
  return (
    <div
      className={cn(
        "grid w-full min-w-0 grid-cols-2 gap-2",
        className,
      )}
      data-testid="research-node-content-faces-surface"
    >
      {CONTENT_FACE_KEYS.map((key) => (
        <div key={key} className="min-w-0" data-content-face={key}>
          <div className="text-[10px] leading-tight text-muted-foreground">
            {t(($) => $.content_faces[faceLabelKey(key)])}
          </div>
          <div className="line-clamp-1 text-xs leading-snug text-foreground">
            {values[key]}
          </div>
        </div>
      ))}
    </div>
  );
}

/** Narrow Git list: single-column four rows (label 80px + value). */
export function ResearchNodeContentFacesStack({
  node,
  className,
}: {
  node: ResearchGraphNode;
  className?: string;
}) {
  const { t } = useT("research");
  const copy = {
    missing: t(($) => $.content_faces.missing),
    resultPending: t(($) => $.content_faces.result_pending),
    resultFailed: t(($) => $.content_faces.result_failed),
  };
  const values = resolveContentFaceValues(node, "surface", copy);

  return (
    <div
      className={cn("flex w-full min-w-0 flex-col gap-1", className)}
      data-testid="research-node-content-faces-stack"
    >
      {CONTENT_FACE_KEYS.map((key) => (
        <div
          key={key}
          className="grid min-w-0 grid-cols-[80px_minmax(0,1fr)] items-baseline gap-x-2"
          data-content-face={key}
        >
          <span className="truncate text-[10px] leading-tight text-muted-foreground">
            {t(($) => $.content_faces[faceLabelKey(key)])}
          </span>
          <span className="line-clamp-1 text-xs leading-snug text-foreground">
            {values[key]}
          </span>
        </div>
      ))}
    </div>
  );
}
