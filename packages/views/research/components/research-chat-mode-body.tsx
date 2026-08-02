"use client";

import { AlertCircle, Loader2, MessageSquare } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { ChatDrawerMode } from "../lib/chat-drawer-mode";

const modeChip: Record<ChatDrawerMode, string> = {
  empty: "border-border/70 bg-muted/40 text-muted-foreground",
  loading: "border-brand/35 bg-brand/10 text-brand",
  running: "border-brand/35 bg-brand/10 text-brand",
  error: "border-destructive/40 bg-destructive/10 text-destructive",
};

/** Mode chip for the chat drawer header (LRM-992). */
export function ResearchChatModeChip({ mode }: { mode: ChatDrawerMode }) {
  const { t } = useT("research");
  const label =
    mode === "empty"
      ? t(($) => $.panel.chat_mode.empty)
      : mode === "loading"
        ? t(($) => $.panel.chat_mode.loading)
        : mode === "error"
          ? t(($) => $.panel.chat_mode.error)
          : t(($) => $.panel.chat_mode.running);

  return (
    <span
      data-testid="research-chat-mode"
      data-chat-mode={mode}
      className={cn(
        "rounded-md border px-1.5 py-0.5 text-[10px] font-medium",
        modeChip[mode],
      )}
    >
      {label}
    </span>
  );
}

/**
 * Designed empty / loading / error bodies for the chat drawer feed (LRM-992).
 * Running mode is owned by the caller's message feed.
 */
export function ResearchChatModeBody({
  mode,
  errorMessage,
  onRetry,
}: {
  mode: Exclude<ChatDrawerMode, "running">;
  errorMessage?: string | null;
  onRetry?: () => void;
}) {
  const { t } = useT("research");

  if (mode === "loading") {
    return (
      <div
        data-testid="research-chat-loading"
        className="space-y-2 px-0.5"
        aria-busy
        aria-live="polite"
      >
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin text-brand" aria-hidden />
          <span>{t(($) => $.chat.loading_body)}</span>
        </div>
        {[0, 1].map((i) => (
          <div
            key={i}
            className="animate-pulse rounded-xl border border-border/50 bg-card/70 p-3"
            style={{ animationDelay: `${i * 80}ms` }}
          >
            <div className="mb-2 flex items-center gap-2">
              <div className="size-6 rounded-full bg-muted/70" />
              <div className="h-2.5 w-[36%] rounded bg-muted/70" />
            </div>
            <div className="mb-1.5 h-2.5 w-full rounded bg-muted/50" />
            <div className="h-2.5 w-[72%] rounded bg-muted/40" />
          </div>
        ))}
      </div>
    );
  }

  if (mode === "error") {
    return (
      <div
        data-testid="research-chat-error"
        role="alert"
        className="rounded-xl border border-destructive/35 bg-destructive/5 px-3 py-3"
      >
        <div className="flex items-start gap-2">
          <AlertCircle className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-destructive">
              {t(($) => $.chat.error_title)}
            </p>
            <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
              {errorMessage || t(($) => $.chat.error_body)}
            </p>
            {onRetry ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="mt-2"
                onClick={onRetry}
              >
                {t(($) => $.session_page.retry)}
              </Button>
            ) : null}
          </div>
        </div>
      </div>
    );
  }

  return (
    <div
      data-testid="research-chat-empty"
      className="rounded-xl border border-border/55 bg-card/80 px-3 py-3"
    >
      <div className="mb-1.5 inline-flex size-8 items-center justify-center rounded-lg border border-border/55 bg-muted/40 text-muted-foreground">
        <MessageSquare className="size-4" aria-hidden />
      </div>
      <p className="text-sm font-medium text-foreground">
        {t(($) => $.chat.empty_title)}
      </p>
      <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
        {t(($) => $.chat.empty_body)}
      </p>
    </div>
  );
}
