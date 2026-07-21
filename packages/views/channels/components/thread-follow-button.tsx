"use client";

import { Button } from "@multica/ui/components/ui/button";
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
      className="h-8 px-2.5 text-xs"
      aria-label={t(($) => followed ? $.thread.unfollow_aria : $.thread.follow_aria)}
      aria-pressed={followed}
      disabled={disabled}
      onClick={() => onFollowChange(!followed)}
    >
      {t(($) => followed ? $.thread.following : $.thread.follow)}
    </Button>
  );
}
