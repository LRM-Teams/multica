"use client";

import { Loader2, Send } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useOpenDM } from "../../common/use-open-dm";
import { useT } from "../../i18n/use-t";

interface AgentProfileMessageButtonProps {
  agentId: string;
  className?: string;
}

/**
 * LRM-283 / LRM-360 · Profile → Message CTA between the agent header and
 * Profile/Activity/Files tabs. Slack-style outline (1px border + background
 * fill) so light and dark both keep ≥4.5:1 label/icon contrast without a
 * solid primary block (corrects LRM-351 overshoot). Offline agents stay
 * tappable; only the in-flight create/find DM blocks re-clicks.
 */
export function AgentProfileMessageButton({
  agentId,
  className,
}: AgentProfileMessageButtonProps) {
  const { t } = useT("agents");
  const { openDM, isPending } = useOpenDM();

  return (
    <div className={cn("shrink-0 px-4 pb-3", className)}>
      <Button
        type="button"
        variant="outline"
        size="lg"
        data-testid="agent-profile-message-button"
        aria-busy={isPending}
        disabled={isPending}
        onClick={() => void openDM({ peer_type: "agent", peer_id: agentId })}
        className="h-9 w-full gap-1.5 rounded-md border-border bg-background font-semibold text-foreground hover:bg-muted dark:border-input dark:bg-background dark:hover:bg-muted disabled:cursor-wait disabled:opacity-100"
      >
        {isPending ? (
          <>
            <Loader2 className="size-4 shrink-0 animate-spin" aria-hidden />
            <span>{t(($) => $.side_panel.message_opening)}</span>
          </>
        ) : (
          <>
            <Send className="size-4 shrink-0" aria-hidden />
            <span>{t(($) => $.side_panel.message_button)}</span>
          </>
        )}
      </Button>
    </div>
  );
}
