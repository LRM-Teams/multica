"use client";

import type { ResearchGraphNode } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useState } from "react";
import { useT } from "../../i18n/use-t";
import {
  CONTENT_FACE_KEYS,
  type ContentFaceDensity,
  type ContentFaceKey,
  resolveContentFaceValues,
} from "../lib/research-node-content-faces";

function ExpandableFaceText({ value }: { value: string }) {
  const { t } = useT("research");
  const [expanded, setExpanded] = useState(false);
  const long = value.length > 280;
  const visible = long && !expanded ? `${value.slice(0, 280).trimEnd()}…` : value;
  return (
    <>
      <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-foreground">
        {visible}
      </p>
      {long ? (
        <button
          type="button"
          className="mt-1 rounded-sm text-xs font-medium text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/30"
          aria-expanded={expanded}
          onClick={() => setExpanded((current) => !current)}
        >
          {expanded ? t(($) => $.node.collapse_content) : t(($) => $.node.expand_content)}
        </button>
      ) : null}
    </>
  );
}

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
            <ExpandableFaceText value={values[key]} />
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
