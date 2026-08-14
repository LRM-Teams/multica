"use client";

import { useEffect, useMemo, useRef } from "react";
import { motion, useAnimationControls } from "motion/react";
import type { LucideIcon } from "lucide-react";
import { useMentionPopupStore } from "@multica/core/inbox";

/**
 * The sidebar inbox icon, wired for the @-mention quick-reply popup:
 *  - publishes its on-screen rect to the shared store so the popup can fly out
 *    of / shrink back into it, and
 *  - bounces when a mention arrives (store `bounceSignal`), signalling that the
 *    popup emerged from the inbox.
 * Rendered only for the inbox nav entry; other icons render plain.
 */
export function InboxNavIcon({
  icon,
  unread = false,
}: {
  icon: LucideIcon;
  unread?: boolean;
}) {
  const ref = useRef<SVGSVGElement>(null);
  const setIconRect = useMentionPopupStore((s) => s.setIconRect);
  const bounceSignal = useMentionPopupStore((s) => s.bounceSignal);
  const controls = useAnimationControls();
  // The lucide icon forwards its ref to the <svg> so we can measure and
  // bounce it. The inline-flex wrapper only positions the collapsed unread
  // dot and does not change the svg box used by the mention popup.
  const MotionIcon = useMemo(() => motion.create(icon), [icon]);

  useEffect(() => {
    const report = () => {
      const el = ref.current;
      if (!el) return;
      const r = el.getBoundingClientRect();
      if (r.width === 0 && r.height === 0) return;
      setIconRect({ x: r.x, y: r.y, width: r.width, height: r.height });
    };
    report();
    window.addEventListener("resize", report);
    window.addEventListener("scroll", report, true);
    return () => {
      window.removeEventListener("resize", report);
      window.removeEventListener("scroll", report, true);
      setIconRect(null);
    };
  }, [setIconRect]);

  useEffect(() => {
    if (bounceSignal === 0) return;
    void controls.start({
      scale: [1, 1.45, 0.9, 1.2, 1],
      rotate: [0, -14, 12, -6, 0],
      transition: { duration: 0.6, ease: "easeInOut" },
    });
  }, [bounceSignal, controls]);

  return (
    <span className="relative inline-flex shrink-0">
      <MotionIcon ref={ref} animate={controls} />
      {unread ? (
        <span
          aria-hidden
          data-testid="inbox-unread-dot"
          className="absolute -top-0.5 -right-0.5 hidden size-1.5 rounded-full bg-brand-solid group-data-[collapsible=icon]:block"
        />
      ) : null}
    </span>
  );
}
