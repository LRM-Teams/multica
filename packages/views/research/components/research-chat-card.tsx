"use client";

import type { ResearchFleetMember, ResearchMessage } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n/use-t";
import { speakerMemberForMessage } from "../lib/research-chat-speaker";

function metaString(meta: unknown, key: string): string | null {
  if (!meta || typeof meta !== "object") return null;
  const value = (meta as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value : null;
}

function formatTime(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

export function ResearchChatCard({
  message,
  members,
}: {
  message: ResearchMessage;
  members: ResearchFleetMember[];
}) {
  const { t } = useT("research");
  const isProcess = message.card_kind === "process";
  const isUser = message.sender_type === "user";
  const member = speakerMemberForMessage(message, members);
  const target = isUser
    ? members.find((m) => m.agent_id === message.target_agent_id)
    : undefined;
  const role = isUser
    ? t(($) => $.chat.from_you)
    : member?.role ?? metaString(message.meta, "op") ?? message.sender_type;
  const name = isUser
    ? t(($) => $.chat.you)
    : member?.display_name ||
      member?.name ||
      (isProcess ? t(($) => $.chat.process) : t(($) => $.chat.system));
  const op = metaString(message.meta, "op");
  const agentId = member?.agent_id;
  const routedTo =
    isUser && message.target_agent_id
      ? target?.display_name || target?.name || t(($) => $.chat.to_lead)
      : null;

  return (
    <article
      className={cn(
        "rounded-xl border px-3 py-2.5 text-sm shadow-sm motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-1",
        isProcess && "border-dashed bg-muted/40",
        isUser && !isProcess && "ml-3 border-primary/25 bg-primary/10",
        !isUser && !isProcess && "mr-1 bg-card",
      )}
    >
      <header className="mb-1.5 flex items-center gap-2">
        {agentId ? (
          <ActorAvatar
            actorType="agent"
            actorId={agentId}
            size={22}
            enableHoverCard
            showStatusDot
            profileLink
          />
        ) : (
          <span
            className={cn(
              "flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full text-[9px] font-semibold uppercase",
              isProcess ? "bg-muted text-muted-foreground" : "bg-primary/15 text-primary",
            )}
          >
            {isUser ? "U" : "S"}
          </span>
        )}
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-medium text-foreground">{name}</div>
          <div className="truncate text-[10px] text-muted-foreground">
            {isProcess ? t(($) => $.chat.process_tag) : role}
            {op && isProcess ? ` · ${op}` : ""}
            {routedTo ? ` · → ${routedTo}` : ""}
            {message.created_at ? ` · ${formatTime(message.created_at)}` : ""}
          </div>
        </div>
      </header>
      <p className="whitespace-pre-wrap text-[13px] leading-relaxed text-foreground/90">{message.body}</p>
    </article>
  );
}
