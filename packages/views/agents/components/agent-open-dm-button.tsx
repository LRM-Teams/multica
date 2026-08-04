"use client";

import type { MouseEvent } from "react";
import { Loader2, MessageSquare } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useOpenDM } from "../../common/use-open-dm";
import { useT } from "../../i18n";

/**
 * LRM-1216 — Agents page list/detail entry into an agent DM.
 * Shared create-or-find + navigate via `useOpenDM` (explicit toast on failure).
 * Not the LRM-283 profile-card Message CTA — keep those surfaces untouched.
 */
export function AgentOpenDmButton({
  agentId,
  variant = "icon",
  className,
}: {
  agentId: string;
  /** `icon` = compact bordered box on list rows; `labeled` = detail header. */
  variant?: "icon" | "labeled";
  className?: string;
}) {
  const { t } = useT("agents");
  const { openDM, isPending } = useOpenDM();
  const aria = t(($) => $.profile_card.send_message);
  const label = isPending
    ? t(($) => $.side_panel.message_opening)
    : t(($) => $.side_panel.message_button);

  const onOpen = (event: MouseEvent) => {
    event.stopPropagation();
    void openDM({ peer_type: "agent", peer_id: agentId });
  };

  if (variant === "labeled") {
    return (
      <Button
        type="button"
        variant="outline"
        size="sm"
        data-testid="agent-open-dm-button"
        aria-label={aria}
        title={aria}
        aria-busy={isPending}
        disabled={isPending}
        onClick={onOpen}
        className={cn("gap-1.5", className)}
      >
        {isPending ? (
          <Loader2 className="size-3.5 shrink-0 animate-spin" aria-hidden />
        ) : (
          <MessageSquare className="size-3.5 shrink-0" aria-hidden />
        )}
        {label}
      </Button>
    );
  }

  return (
    <Button
      type="button"
      variant="outline"
      size="icon-sm"
      data-testid="agent-open-dm-button"
      aria-label={aria}
      title={aria}
      aria-busy={isPending}
      disabled={isPending}
      onClick={onOpen}
      className={cn(
        "size-8 shrink-0 rounded-md border-border bg-background text-muted-foreground hover:bg-muted hover:text-foreground",
        className,
      )}
    >
      {isPending ? (
        <Loader2 className="size-3.5 animate-spin" aria-hidden />
      ) : (
        <MessageSquare className="size-3.5" aria-hidden />
      )}
    </Button>
  );
}
