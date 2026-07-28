"use client";

import { useState, useSyncExternalStore } from "react";
import {
  getWebNotificationPermission,
  isWebNotificationSupported,
  type WebNotificationPermission,
} from "@multica/core/platform";
import {
  getWebPushSupportState,
  requestAndBindWebPushSubscription,
} from "@multica/core/web-push";
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
  const clientReady = useSyncExternalStore(
    () => () => undefined,
    () => true,
    () => false,
  );
  const [, refresh] = useState(0);
  const [busy, setBusy] = useState(false);
  const permission: WebNotificationPermission = getWebNotificationPermission();
  const pushState = getWebPushSupportState();

  // Pre-hydration or desktop -> nothing to manage. On iOS Safari we still render
  // the Home Screen install guidance even before Notification is exposed.
  if (!clientReady || isDesktopShell()) return null;
  if (!isWebNotificationSupported() && pushState !== "ios_requires_pwa") return null;

  const handleEnable = async () => {
    setBusy(true);
    try {
      await requestAndBindWebPushSubscription();
    } catch (err) {
      // Permission denial is reflected by Notification.permission below, but a
      // subscribe/bind failure (network / CSRF / server error) is NOT — the
      // browser can hold a subscription while the server has no row, leaving
      // background push silently broken ("foreground works, background doesn't").
      // Surface it so DevTools / QA can diagnose.
      console.error("[web-push] enable failed (subscribe/bind)", err);
    } finally {
      refresh((version) => version + 1);
      setBusy(false);
    }
  };

  const statusHint =
    pushState === "ios_requires_pwa"
      ? t(($) => $.notifications.browser.ios_requires_pwa)
      : pushState === "unsupported"
        ? t(($) => $.notifications.browser.unsupported)
        : permission === "granted"
          ? t(($) => $.notifications.browser.granted)
          : permission === "denied"
            ? t(($) => $.notifications.browser.denied)
            : t(($) => $.notifications.browser.hint);

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
          {permission !== "granted" && pushState === "supported" && (
            <Button size="sm" variant="outline" onClick={handleEnable} disabled={busy}>
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
