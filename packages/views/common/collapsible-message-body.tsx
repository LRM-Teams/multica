"use client";

import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { cn } from "@multica/ui/lib/utils";
import {
  messageCollapseFadeClassName,
  type MessageCollapseFadeVariant,
} from "./mention-token";

/** Slack-style collapsed body height (LRM-268 / channel bubbles). */
export const MESSAGE_COLLAPSE_MAX_HEIGHT_PX = 160;
const MESSAGE_COLLAPSE_HEIGHT_CLASS = "max-h-[160px]";
const MESSAGE_COLLAPSE_OVERFLOW_EPSILON_PX = 2;

/**
 * Long chat / channel bodies: clip to Slack height with an explicit
 * 「查看更多」/「收起」control. Full DOM stays mounted so expand is instant
 * and copy still sees the complete text.
 */
export function CollapsibleMessageBody({
  children,
  contentKey,
  enabled = true,
  expandLabel,
  collapseLabel,
  fadeVariant = "default",
  className,
}: {
  children: ReactNode;
  /** Remeasure when the underlying message identity/body changes. */
  contentKey: string;
  enabled?: boolean;
  expandLabel: string;
  collapseLabel: string;
  fadeVariant?: MessageCollapseFadeVariant;
  className?: string;
}) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const measureRef = useRef<() => void>(() => {});
  const [contentOverflows, setContentOverflows] = useState(false);
  const [expanded, setExpanded] = useState(false);

  const measureContentOverflow = useCallback(() => {
    const body = bodyRef.current;
    if (!body || !enabled) {
      setContentOverflows(false);
      return;
    }
    const overflows =
      body.scrollHeight > MESSAGE_COLLAPSE_MAX_HEIGHT_PX + MESSAGE_COLLAPSE_OVERFLOW_EPSILON_PX;
    setContentOverflows((previous) => (previous === overflows ? previous : overflows));
  }, [enabled]);
  measureRef.current = measureContentOverflow;

  useLayoutEffect(() => {
    if (!enabled) {
      setContentOverflows(false);
      return;
    }
    measureRef.current();
  }, [enabled, contentKey, expanded]);

  useEffect(() => {
    const handleOverflow = () => measureRef.current();
    window.addEventListener("resize", handleOverflow);
    return () => window.removeEventListener("resize", handleOverflow);
  }, []);

  // New message → start collapsed again.
  useEffect(() => {
    setExpanded(false);
  }, [contentKey]);

  const canCollapse = enabled && contentOverflows;
  const isCollapsed = canCollapse && !expanded;

  return (
    <div className={cn("relative min-w-0", className)}>
      <div
        ref={bodyRef}
        className={cn(
          isCollapsed && "overflow-hidden",
          isCollapsed ? MESSAGE_COLLAPSE_HEIGHT_CLASS : "overflow-visible",
        )}
        data-testid="collapsible-message-body"
        data-collapsed={isCollapsed ? "true" : undefined}
      >
        {children}
        {isCollapsed && (
          <div
            className={messageCollapseFadeClassName(fadeVariant)}
            data-testid="message-collapse-fade"
          >
            <button
              type="button"
              className="pointer-events-auto inline-flex min-h-8 items-center px-0 text-sm font-normal text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              onClick={() => setExpanded(true)}
            >
              {expandLabel}
            </button>
          </div>
        )}
      </div>
      {canCollapse && !isCollapsed && (
        <div className="mt-1 flex justify-start" data-testid="message-collapse-less">
          <button
            type="button"
            className="inline-flex min-h-8 items-center px-0 text-sm font-normal text-primary underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            onClick={() => setExpanded(false)}
          >
            {collapseLabel}
          </button>
        </div>
      )}
    </div>
  );
}
