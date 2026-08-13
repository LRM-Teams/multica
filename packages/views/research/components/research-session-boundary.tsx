"use client";

import { Fragment, type ReactNode } from "react";

/**
 * Session id is an ownership boundary for every local hook below it. Changing
 * the id remounts descendants so drafts, refs, mutation observers, and overlays
 * from one Research session cannot act on the next session.
 */
export function ResearchSessionBoundary({
  sessionId,
  children,
}: {
  sessionId: string;
  children: ReactNode;
}) {
  return <Fragment key={sessionId}>{children}</Fragment>;
}
