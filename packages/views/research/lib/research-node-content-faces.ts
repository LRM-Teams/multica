/**
 * LRM-1332 / LRM-1309 — read-only projection for the four node content faces.
 *
 * Consumes only the BE-projected `content.*` fields (LRM-1317). Never falls
 * back to `summary`, payload scans, status colors, or error/task codes.
 */

import type { ResearchGraphNode } from "@multica/core/types";

export const CONTENT_FACE_KEYS = [
  "goal",
  "operation_approach",
  "research_approach",
  "result",
] as const;

export type ContentFaceKey = (typeof CONTENT_FACE_KEYS)[number];

export type ResearchNodeContentFaces = Record<ContentFaceKey, string>;

export type ContentFaceDensity = "surface" | "detail";

/** Projected shape on graph-tree nodes (excess field vs core type until FE sync). */
type NodeWithContentProjection = ResearchGraphNode & {
  content?: Partial<Record<ContentFaceKey, unknown>> | null;
};

function trimFace(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

/** Read projected faces only — empty string means “not provided”. */
export function readNodeContentFaces(node: ResearchGraphNode): ResearchNodeContentFaces {
  const content = (node as NodeWithContentProjection).content;
  return {
    goal: trimFace(content?.goal),
    operation_approach: trimFace(content?.operation_approach),
    research_approach: trimFace(content?.research_approach),
    result: trimFace(content?.result),
  };
}

function isRunningStatus(status: string): boolean {
  const s = status.toLowerCase();
  return s === "active" || s === "running" || s === "in_progress";
}

function isFailedOrUnknownStatus(status: string): boolean {
  const s = status.toLowerCase();
  return s === "failed" || s === "error" || s === "unknown";
}

export type ContentFaceCopy = {
  missing: string;
  resultPending: string;
  resultFailed: string;
};

/**
 * Resolve display text for one face. Raw values pass through unchanged
 * (CSS clamp truncates on the surface; detail keeps full text).
 */
export function resolveContentFaceValue(
  key: ContentFaceKey,
  raw: string,
  nodeStatus: string,
  copy: ContentFaceCopy,
): string {
  if (raw) return raw;
  if (key === "result") {
    if (isRunningStatus(nodeStatus)) return copy.resultPending;
    if (isFailedOrUnknownStatus(nodeStatus)) return copy.resultFailed;
  }
  return copy.missing;
}

/** `density` is reserved for callers that vary copy; resolution uses `copy` only. */
export function resolveContentFaceValues(
  node: ResearchGraphNode,
  _density: ContentFaceDensity,
  copy: ContentFaceCopy,
): ResearchNodeContentFaces {
  const faces = readNodeContentFaces(node);
  return {
    goal: resolveContentFaceValue("goal", faces.goal, node.status, copy),
    operation_approach: resolveContentFaceValue(
      "operation_approach",
      faces.operation_approach,
      node.status,
      copy,
    ),
    research_approach: resolveContentFaceValue(
      "research_approach",
      faces.research_approach,
      node.status,
      copy,
    ),
    result: resolveContentFaceValue("result", faces.result, node.status, copy),
  };
}

/** Chinese phrases for buildNodeAccessibleName (matches existing ZH helpers). */
export const CONTENT_FACE_A11Y_ZH: Record<ContentFaceKey, string> = {
  goal: "目标",
  operation_approach: "操作思路",
  research_approach: "调研思路",
  result: "调研结果",
};

export const CONTENT_FACE_COPY_ZH: ContentFaceCopy = {
  missing: "未提供",
  resultPending: "结果整理中",
  resultFailed: "本轮未产出可展示结果",
};
