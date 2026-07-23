"use client";

import { useEffect, useState } from "react";
import { X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

/** One-shot tip: @agent trigger copy lives here, not in the Slack-short placeholder (LRM-491). */
export const COMPOSER_MENTION_HINT_LS_KEY = "multica:composer-mention-hint-dismissed";

export function ComposerMentionHint({ className }: { className?: string }) {
  const { t } = useT("channels");
  const [visible, setVisible] = useState<boolean | null>(null);

  useEffect(() => {
    try {
      setVisible(window.localStorage.getItem(COMPOSER_MENTION_HINT_LS_KEY) !== "true");
    } catch {
      setVisible(false);
    }
  }, []);

  if (visible !== true) return null;

  const dismiss = () => {
    try {
      window.localStorage.setItem(COMPOSER_MENTION_HINT_LS_KEY, "true");
    } catch {
      // ignore quota / private mode
    }
    setVisible(false);
  };

  return (
    <div
      role="status"
      data-slot="composer-mention-hint"
      className={cn(
        "mb-2 flex items-start gap-2 rounded-md border border-border/60 bg-muted/40 px-3 py-2 text-[13px] leading-snug text-muted-foreground",
        className,
      )}
    >
      <p className="min-w-0 flex-1">{t(($) => $.composer.mention_hint)}</p>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        className="shrink-0 text-muted-foreground hover:text-foreground"
        aria-label={t(($) => $.composer.mention_hint_dismiss)}
        onClick={dismiss}
      >
        <X className="size-3.5" />
      </Button>
    </div>
  );
}
