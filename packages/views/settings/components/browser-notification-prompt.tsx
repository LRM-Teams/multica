"use client";

import { useEffect, useState } from "react";
import {
  dismissBrowserNotificationPrompt,
  getBrowserNotificationCapability,
  getWebNotificationPermission,
  requestWebNotificationPermission,
  shouldShowBrowserNotificationPrompt,
  snoozeBrowserNotificationPrompt,
} from "@multica/core/platform";
import { Button } from "@multica/ui/components/ui/button";
import { Bell, X } from "lucide-react";
import { isDesktopShell } from "../../platform";
import { useT } from "../../i18n";

/**
 * Slack-style soft-ask (LRM-487 / LRM-525): a light top banner — not a modal —
 * asking to enable system notification banners for DMs and group @mentions.
 * Hidden on desktop Electron (OS banners) and when permission is already decided.
 *
 * Two primary actions: enable + later. Permanent dismiss lives on ✕ / repeated
 * snoozes (core storage), not a third heavy button.
 */
export function BrowserNotificationPrompt() {
  const { t } = useT("settings");
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (isDesktopShell()) return;
    const permission = getWebNotificationPermission();
    const capability = getBrowserNotificationCapability();
    // Delay slightly so the dashboard settles before the banner appears.
    const timer = window.setTimeout(() => {
      setOpen(shouldShowBrowserNotificationPrompt(permission, capability));
    }, 800);
    return () => window.clearTimeout(timer);
  }, []);

  if (isDesktopShell() || !open) return null;

  const handleEnable = async () => {
    await requestWebNotificationPermission();
    dismissBrowserNotificationPrompt();
    setOpen(false);
  };

  const handleLater = () => {
    snoozeBrowserNotificationPrompt();
    setOpen(false);
  };

  const handleDismiss = () => {
    dismissBrowserNotificationPrompt();
    setOpen(false);
  };

  return (
    <section
      aria-label={t(($) => $.notifications.browser.prompt.title)}
      data-testid="browser-notification-prompt"
      className="shrink-0 border-b border-border bg-muted/60 px-3 py-2.5 sm:px-4"
    >
      <div className="mx-auto flex w-full max-w-5xl flex-col gap-2.5 sm:flex-row sm:items-center sm:gap-3">
        <div className="flex min-w-0 flex-1 items-start gap-2.5 sm:items-center">
          <div
            className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full bg-brand/10 text-brand sm:mt-0"
            aria-hidden
          >
            <Bell className="size-4" />
          </div>
          <p className="min-w-0 flex-1 text-sm leading-snug text-foreground">
            {t(($) => $.notifications.browser.prompt.description)}
          </p>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-8 shrink-0 text-muted-foreground hover:text-foreground sm:hidden"
            aria-label={t(($) => $.notifications.browser.prompt.dismiss)}
            onClick={handleDismiss}
          >
            <X className="size-4" />
          </Button>
        </div>

        <div className="flex items-center gap-1.5 sm:shrink-0">
          <Button
            type="button"
            data-testid="browser-notification-prompt-enable"
            className="h-11 min-h-11 flex-1 rounded-[10px] border-0 bg-brand px-4 font-[650] text-brand-foreground shadow-none hover:bg-brand/90 focus-visible:border-transparent focus-visible:ring-brand/35 sm:flex-none"
            onClick={() => void handleEnable()}
          >
            {t(($) => $.notifications.browser.prompt.enable)}
          </Button>
          <Button
            type="button"
            variant="ghost"
            data-testid="browser-notification-prompt-later"
            className="h-11 min-h-11 flex-1 rounded-[10px] px-3 text-sm font-medium text-foreground sm:flex-none"
            onClick={handleLater}
          >
            {t(($) => $.notifications.browser.prompt.later)}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="hidden size-8 shrink-0 text-muted-foreground hover:text-foreground sm:inline-flex"
            aria-label={t(($) => $.notifications.browser.prompt.dismiss)}
            data-testid="browser-notification-prompt-dismiss"
            onClick={handleDismiss}
          >
            <X className="size-4" />
          </Button>
        </div>
      </div>
    </section>
  );
}
