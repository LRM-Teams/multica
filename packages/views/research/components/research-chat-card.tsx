"use client";

import type {
  ResearchClarificationQuestion,
  ResearchFleetMember,
  ResearchMessage,
  ResearchProductRoundCard,
} from "@multica/core/types";
import { StreamingMarkdown } from "@multica/ui/markdown";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { useT } from "../../i18n/use-t";
import {
  parseClarificationQuestion,
  resolveClarificationResolution,
  type ClarificationResolution,
} from "../lib/clarification-question";
import { speakerMemberForMessage } from "../lib/research-chat-speaker";
import { productRoundCardFromProcessMessage } from "../lib/product-round-process-card";
import { ResearchClarificationCard } from "./research-clarification-card";
import { ResearchProductRoundCardView } from "./research-product-round-card";

function metaString(meta: unknown, key: string): string | null {
  if (!meta || typeof meta !== "object") return null;
  const value = (meta as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value : null;
}

function metaBool(meta: unknown, key: string): boolean {
  if (!meta || typeof meta !== "object") return false;
  return (meta as Record<string, unknown>)[key] === true;
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
  messages,
  currentGoal,
  onRoundAgree,
  onRoundRejectContinue,
  onRoundRejectStop,
  onConfirmGoalPatch,
  onRejectGoalPatch,
  onEditGoalPatch,
  onClarificationOption,
  onClarificationForm,
  onClarificationSkip,
  roundPending,
  clarificationPending,
}: {
  message: ResearchMessage;
  members: ResearchFleetMember[];
  /** Full feed — used to resolve clarification answered/skipped state (LRM-822). */
  messages?: ResearchMessage[];
  currentGoal?: string;
  onRoundAgree?: (card: ResearchProductRoundCard) => void | Promise<void>;
  onRoundRejectContinue?: (card: ResearchProductRoundCard) => void | Promise<void>;
  onRoundRejectStop?: (card: ResearchProductRoundCard) => void | Promise<void>;
  onConfirmGoalPatch?: (card: ResearchProductRoundCard, text: string) => void | Promise<void>;
  onRejectGoalPatch?: (card: ResearchProductRoundCard) => void | Promise<void>;
  onEditGoalPatch?: (card: ResearchProductRoundCard, text: string) => void | Promise<void>;
  onClarificationOption?: (
    question: ResearchClarificationQuestion,
    optionId: string,
  ) => void;
  onClarificationForm?: (
    question: ResearchClarificationQuestion,
    values: Record<string, string>,
  ) => void;
  onClarificationSkip?: (question: ResearchClarificationQuestion) => void;
  roundPending?: boolean;
  clarificationPending?: boolean;
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
  const roundCard = isProcess ? productRoundCardFromProcessMessage(message) : null;
  const clarification = !isUser ? parseClarificationQuestion(message) : null;
  const clarificationResolution: ClarificationResolution = clarification
    ? resolveClarificationResolution(clarification, messages ?? [message])
    : { status: "pending" };
  const wasStopped = metaBool(message.meta, "stopped");
  const useMarkdown = !isUser && !isProcess && !roundCard && !clarification;

  return (
    <article
      className={cn(
        "rounded-xl border px-3 py-2.5 text-sm shadow-sm motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-1",
        isProcess && "border-dashed bg-muted/40",
        isUser && !isProcess && "ml-3 border-primary/25 bg-primary/10",
        !isUser && !isProcess && "mr-1 bg-card",
        wasStopped && "border-warning/30",
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
            {wasStopped ? ` · ${t(($) => $.chat.stopped_tag)}` : ""}
            {message.created_at ? ` · ${formatTime(message.created_at)}` : ""}
          </div>
        </div>
      </header>
      {roundCard ? (
        <div className="space-y-2">
          <p className="whitespace-pre-wrap text-[13px] leading-relaxed text-foreground/90">
            {message.body}
          </p>
          <ResearchProductRoundCardView
            card={roundCard}
            currentGoal={currentGoal}
            compact
            pending={roundPending}
            onAgree={() => onRoundAgree?.(roundCard)}
            onRejectContinue={() => onRoundRejectContinue?.(roundCard)}
            onRejectStop={() => onRoundRejectStop?.(roundCard)}
            onConfirmGoalPatch={(text) => onConfirmGoalPatch?.(roundCard, text)}
            onRejectGoalPatch={() => onRejectGoalPatch?.(roundCard)}
            onEditGoalPatch={(text) => onEditGoalPatch?.(roundCard, text)}
          />
        </div>
      ) : clarification ? (
        <div className="w-full min-w-0 space-y-2">
          {message.body && message.body.trim() !== clarification.prompt ? (
            <p className="whitespace-pre-wrap text-[13px] leading-relaxed text-foreground/90">
              {message.body}
            </p>
          ) : null}
          <ResearchClarificationCard
            question={clarification}
            resolution={clarificationResolution}
            pending={clarificationPending}
            onSelectOption={(optionId) =>
              onClarificationOption?.(clarification, optionId)
            }
            onSubmitForm={(values) => onClarificationForm?.(clarification, values)}
            onSkip={() => onClarificationSkip?.(clarification)}
          />
        </div>
      ) : useMarkdown ? (
        <div className="text-[13px] leading-relaxed text-foreground/90">
          <StreamingMarkdown content={message.body} isStreaming={false} />
        </div>
      ) : (
        <p className="whitespace-pre-wrap text-[13px] leading-relaxed text-foreground/90">
          {message.body}
        </p>
      )}
    </article>
  );
}
