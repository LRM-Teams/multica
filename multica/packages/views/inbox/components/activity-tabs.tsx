"use client";

import { Activity } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import type { UserActivityTab } from "@multica/core/types";
import { useT } from "../../i18n";

const TABS: UserActivityTab[] = ["all", "unread", "mentions"];

export function ActivityTabs({
  value,
  onChange,
  unreadCount,
}: {
  value: UserActivityTab;
  onChange: (tab: UserActivityTab) => void;
  unreadCount: number;
}) {
  const { t } = useT("inbox");

  return (
    <div
      className="flex shrink-0 gap-0 border-b border-border px-4"
      role="tablist"
      aria-label={t(($) => $.activity.tabs_label)}
    >
      {TABS.map((tab) => {
        const active = value === tab;
        const showUnreadBadge = tab === "unread" && unreadCount > 0;
        return (
          <button
            key={tab}
            type="button"
            role="tab"
            aria-selected={active}
            onClick={() => onChange(tab)}
            className={cn(
              "-mb-px border-b-2 px-3.5 py-2.5 text-sm font-semibold transition-colors",
              active
                ? "border-brand text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            {t(($) => $.activity.tabs[tab])}
            {showUnreadBadge ? (
              <span className="ml-1 inline-flex rounded-full bg-brand/15 px-1.5 py-0.5 text-[11px] font-bold text-brand">
                {unreadCount}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

export function ActivityEmptyState({ tab }: { tab: UserActivityTab }) {
  const { t } = useT("inbox");
  return (
    <div className="flex flex-col items-center justify-center px-6 py-16 text-center text-muted-foreground">
      <Activity className="mb-3 h-8 w-8 text-muted-foreground/50" />
      <p className="text-sm font-semibold text-foreground">
        {t(($) => $.activity.empty[tab].title)}
      </p>
      <p className="mt-1 max-w-sm text-sm leading-relaxed">
        {t(($) => $.activity.empty[tab].description)}
      </p>
    </div>
  );
}
