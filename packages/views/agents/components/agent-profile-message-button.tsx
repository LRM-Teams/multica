"use client";

import { Loader2, Send } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useOpenDM } from "../../common/use-open-dm";
import { useT } from "../../i18n/use-t";

interface AgentProfileMessageButtonProps {
  agentId: string;
  className?: string;
}

/**
 * LRM-283 · Slack Profile → Message: full-width outlined primary between the
 * agent header and Profile/Activity/Files tabs. Offline agents stay tappable;
 * only the in-flight create/find DM blocks re-clicks.
 */
export function AgentProfileMessageButton({
  agentId,
  className,
}: AgentProfileMessageButtonProps) {
  const { t } = useT("agents");
  const { openDM, isPending } = useOpenDM();

  return (
    <div className={cn("shrink-0 px-4 pb-3", className)}>
      <button
        type="button"
        data-testid="agent-profile-message-button"
        aria-busy={isPending}
        disabled={isPending}
        onClick={() => void openDM({ peer_type: "agent", peer_id: agentId })}
        className={cn(
          "flex h-9 w-full items-center justify-center gap-2 rounded-md border border-[rgba(29,28,29,0.3)] bg-background text-sm font-bold text-foreground transition-colors",
          "hover:bg-[#f4f4f4] disabled:cursor-wait",
        )}
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
      </button>
    </div>
  );
}
