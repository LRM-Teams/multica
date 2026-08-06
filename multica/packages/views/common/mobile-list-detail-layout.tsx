import type { ReactNode } from "react";
import { cn } from "@multica/ui/lib/utils";

type MobileListDetailLayoutProps = {
  /** When true, `detail` is shown full-screen and `list` stays mounted off-screen. */
  showDetail: boolean;
  list: ReactNode;
  detail: ReactNode;
  className?: string;
};

/**
 * Mobile list ↔ detail shell shared by inbox and channels.
 *
 * Both panes stay mounted so the list's `overflow-y-auto` scroll offset survives
 * a round trip through detail. The hidden list is parked with
 * `invisible absolute` (not `display: none`) so the browser keeps its scroll
 * position without painting or receiving pointer events.
 */
export function MobileListDetailLayout({
  showDetail,
  list,
  detail,
  className,
}: MobileListDetailLayoutProps) {
  return (
    <div className={cn("relative flex min-h-0 flex-1 flex-col", className)}>
      <div
        className={cn(
          "flex min-h-0 flex-1 flex-col",
          showDetail &&
            "pointer-events-none invisible absolute inset-0 z-0 overflow-hidden",
        )}
        aria-hidden={showDetail}
      >
        {list}
      </div>
      {showDetail ? (
        <div className="relative z-10 flex min-h-0 flex-1 flex-col">{detail}</div>
      ) : null}
    </div>
  );
}