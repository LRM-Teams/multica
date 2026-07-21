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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Bell } from "lucide-react";
import { isDesktopShell } from "../../platform";
import { useT } from "../../i18n";

/**
 * WeChat-style soft prompt: ask once (per browser) to enable system
 * notification banners for DMs and group @mentions. Hidden on desktop
 * (Electron uses OS banners) and when permission is already decided.
 */
export function BrowserNotificationPrompt() {
  const { t } = useT("settings");
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (isDesktopShell()) return;
    const permission = getWebNotificationPermission();
    const capability = getBrowserNotificationCapability();
    // Delay slightly so the dashboard settles before the modal appears.
    const timer = window.setTimeout(() => {
      setOpen(shouldShowBrowserNotificationPrompt(permission, capability));
    }, 800);
    return () => window.clearTimeout(timer);
  }, []);

  if (isDesktopShell()) return null;

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
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) handleLater();
      }}
    >
      <DialogContent className="gap-0 overflow-hidden p-0" showCloseButton={false}>
        <DialogHeader className="items-center gap-3 px-6 pt-8 text-center sm:text-center">
          <div className="flex size-14 items-center justify-center rounded-full bg-primary/10 text-primary">
            <Bell className="size-7" aria-hidden />
          </div>
          <DialogTitle className="text-lg">
            {t(($) => $.notifications.browser.prompt.title)}
          </DialogTitle>
          <DialogDescription className="pb-2 text-sm leading-relaxed text-muted-foreground">
            {t(($) => $.notifications.browser.prompt.description)}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter className="mx-0 mb-0 flex-col gap-2 rounded-none border-0 bg-transparent p-4 sm:flex-col sm:justify-stretch sm:space-x-0">
          <Button className="w-full" onClick={() => void handleEnable()}>
            {t(($) => $.notifications.browser.prompt.enable)}
          </Button>
          <Button className="w-full" variant="ghost" onClick={handleLater}>
            {t(($) => $.notifications.browser.prompt.later)}
          </Button>
          <button
            type="button"
            className="pt-1 text-xs text-muted-foreground underline-offset-2 hover:underline"
            onClick={handleDismiss}
          >
            {t(($) => $.notifications.browser.prompt.dismiss)}
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
