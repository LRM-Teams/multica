"use client";

import { Bot, ExternalLink, Sparkles, MonitorSmartphone } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { useNavigation } from "../navigation";

/**
 * First-open / setup hint for the Notes FAB bubble's dedicated 笔记助手.
 * Create only on button click: clone Wendy's Computer+runtime, or open the
 * create-agent dialog (identity prefilled; human picks Computer+runtime).
 */
export function NotesAssistantSetupCard({
  needsSetup,
  onboardingAvailable,
  ensuring,
  settingsHref,
  onCloneOnboarding,
  onOpenManualCreate,
  onDismiss,
  className,
}: {
  needsSetup: boolean;
  onboardingAvailable: boolean;
  ensuring?: boolean;
  settingsHref: string;
  onCloneOnboarding: () => void;
  onOpenManualCreate: () => void;
  onDismiss?: () => void;
  className?: string;
}) {
  const { t } = useT("layout");
  const { push } = useNavigation();

  return (
    <div
      className={cn(
        "not-prose mx-auto my-3 w-full max-w-md overflow-hidden rounded-xl border bg-card text-card-foreground shadow-sm",
        className,
      )}
      data-testid="notes-assistant-setup-card"
      data-needs-setup={needsSetup ? "true" : "false"}
    >
      <div className="border-b bg-muted/30 px-4 py-3">
        <div className="flex items-start gap-3">
          <div className="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-background text-muted-foreground">
            <Bot className="size-4" />
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-xs font-medium text-muted-foreground">
              {t(($) => $.notes_page.assistant_setup_badge)}
            </p>
            <p className="mt-0.5 text-sm font-semibold leading-snug">
              {needsSetup
                ? t(($) => $.notes_page.assistant_setup_title_needed)
                : t(($) => $.notes_page.assistant_setup_title_ready)}
            </p>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">
              {needsSetup
                ? t(($) => $.notes_page.assistant_setup_body_needed)
                : t(($) => $.notes_page.assistant_setup_body_ready)}
            </p>
          </div>
        </div>
      </div>
      <div className="flex flex-col gap-2 px-4 py-3">
        <Button
          type="button"
          size="sm"
          disabled={ensuring || !onboardingAvailable}
          onClick={onCloneOnboarding}
          className="justify-start gap-2"
        >
          <Sparkles className="size-3.5" />
          {t(($) => $.notes_page.assistant_setup_clone_wendy)}
        </Button>
        {!onboardingAvailable ? (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.notes_page.assistant_setup_wendy_missing)}
          </p>
        ) : null}
        {needsSetup ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="justify-start gap-2"
            disabled={ensuring}
            onClick={onOpenManualCreate}
          >
            <MonitorSmartphone className="size-3.5" />
            {t(($) => $.notes_page.assistant_setup_choose_runtime)}
          </Button>
        ) : (
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="justify-start gap-2"
            onClick={() => push(settingsHref)}
          >
            <ExternalLink className="size-3.5" />
            {t(($) => $.notes_page.assistant_setup_open_members)}
          </Button>
        )}
        {!needsSetup && onDismiss ? (
          <Button type="button" size="sm" variant="ghost" onClick={onDismiss}>
            {t(($) => $.notes_page.assistant_setup_dismiss)}
          </Button>
        ) : null}
      </div>
    </div>
  );
}
