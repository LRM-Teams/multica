"use client";

import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

interface ThreadFollowButtonProps {
  followed: boolean;
  disabled?: boolean;
  onFollowChange: (followed: boolean) => void;
}

/** Minimal explicit thread subscription control shared by group and DM threads. */
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
        // LRM-572 / LRM-568 — text control ≥32px; Slack-like brand outline when idle.
        "h-8 min-h-8 min-w-8 px-2.5 text-xs font-medium",
        followed
          ? "border border-transparent bg-muted text-muted-foreground hover:bg-muted/80 hover:text-foreground"
          : "border border-border text-primary hover:bg-accent hover:text-primary",
      )}
      aria-label={t(($) => followed ? $.thread.unfollow_aria : $.thread.follow_aria)}
      aria-pressed={followed}
      disabled={disabled}
      onClick={() => onFollowChange(!followed)}
    >
      {t(($) => followed ? $.thread.following : $.thread.follow)}
    </Button>
  );
}
