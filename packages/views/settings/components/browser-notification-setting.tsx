"use client";

import { useEffect, useState } from "react";
import {
  getBrowserNotificationCapability,
  getWebNotificationPermission,
  requestWebNotificationPermission,
  type BrowserNotificationCapability,
  type WebNotificationPermission,
} from "@multica/core/platform";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { isDesktopShell } from "../../platform";
import { useT } from "../../i18n";

/**
 * Web-only control for the browser permission that native notification banners
 * require. Desktop delivers banners through the OS via Electron (no browser
 * permission involved), so this renders nothing there. It also renders nothing
 * when the Notification API is unavailable (SSR, older browsers).
 *
 * Capability and permission are read from `window`, so the first paint defers
 * to a post-mount effect to keep SSR and client markup identical (no hydration
 * mismatch).
 */
export function BrowserNotificationSetting() {
  const { t } = useT("settings");
  const [mounted, setMounted] = useState(false);
  const [permission, setPermission] =
    useState<WebNotificationPermission>("default");
  const [capability, setCapability] =
    useState<BrowserNotificationCapability>("unsupported");

  useEffect(() => {
    setMounted(true);
    setPermission(getWebNotificationPermission());
    setCapability(getBrowserNotificationCapability());
  }, []);

  // Pre-mount or desktop: desktop uses OS-native delivery, not browser prompts.
  if (!mounted || isDesktopShell()) return null;

  const handleEnable = async () => {
    setPermission(await requestWebNotificationPermission());
  };

  const statusHint =
    capability === "unsupported"
      ? t(($) => $.notifications.browser.unsupported)
      : capability === "ios-needs-pwa"
        ? t(($) => $.notifications.browser.ios_needs_pwa)
        : permission === "granted"
          ? t(($) => $.notifications.browser.granted)
          : permission === "denied"
            ? t(($) => $.notifications.browser.denied)
            : capability === "ios-pwa"
              ? t(($) => $.notifications.browser.ios_pwa_hint)
              : t(($) => $.notifications.browser.hint);

  const canRequestPermission =
    capability === "standard" || capability === "ios-pwa";

  return (
    <Card>
      <CardContent>
        <div className="flex items-center justify-between gap-4">
          <div className="space-y-0.5 pr-4">
            <p className="text-sm font-medium">
              {t(($) => $.notifications.browser.label)}
            </p>
            <p className="text-xs text-muted-foreground">{statusHint}</p>
          </div>
          {permission === "default" && canRequestPermission && (
            <Button size="sm" variant="outline" onClick={handleEnable}>
              {t(($) => $.notifications.browser.enable)}
            </Button>
          )}
          {permission === "granted" && (
            <span className="shrink-0 text-xs font-medium text-muted-foreground">
              {t(($) => $.notifications.browser.enabled_badge)}
            </span>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
