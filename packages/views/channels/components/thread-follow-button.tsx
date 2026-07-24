"use client";

import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

interface ThreadFollowButtonProps {
  followed: boolean;
  disabled?: boolean;
  onFollowChange: (followed: boolean) => void;
}

/**
 * Minimal explicit thread subscription control shared by group and DM threads.
 * LRM-572 — text「跟随 / 已跟随」; touch target ≥32px (Slack-style bordered chip).
 */
export function ThreadFollowButton({
  followed,
  disabled = false,
  onFollowChange,
}: ThreadFollowButtonProps) {
  const { t } = useT("channels");

  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className={cn(
        "h-8 min-h-8 px-2.5 text-xs font-medium",
        followed
          ? "border-transparent bg-muted text-muted-foreground hover:bg-muted/80"
          : "border border-border text-primary hover:bg-primary/5",
      )}
      aria-label={t(($) => (followed ? $.thread.unfollow_aria : $.thread.follow_aria))}
      aria-pressed={followed}
      disabled={disabled}
      onClick={() => onFollowChange(!followed)}
    >
      {t(($) => (followed ? $.thread.following : $.thread.follow))}
    </Button>
  );
}
