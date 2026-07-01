"use client";

import { useEffect, useState } from "react";
import {
  getWebNotificationPermission,
  isWebNotificationSupported,
  type WebNotificationPermission,
} from "@multica/core/platform";
import {
  bindCurrentWebPushSubscription,
  getWebPushSupportState,
  requestAndBindWebPushSubscription,
  unbindCurrentWebPushSubscription,
  type WebPushSupportState,
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
  const [mounted, setMounted] = useState(false);
  const [permission, setPermission] =
    useState<WebNotificationPermission>("default");
  const [pushState, setPushState] = useState<WebPushSupportState>("unsupported");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setMounted(true);
    setPermission(getWebNotificationPermission());
    setPushState(getWebPushSupportState());
  }, []);

  useEffect(() => {
    if (!mounted) return;
    if (permission === "denied") {
      unbindCurrentWebPushSubscription().catch(() => undefined);
      return;
    }
    if (permission !== "granted" || pushState !== "supported") return;
    bindCurrentWebPushSubscription().catch(() => undefined);
  }, [mounted, permission, pushState]);

  // Pre-mount or desktop -> nothing to manage. On iOS Safari we still render
  // the Home Screen install guidance even before Notification is exposed.
  if (!mounted || isDesktopShell()) return null;
  if (!isWebNotificationSupported() && pushState !== "ios_requires_pwa") return null;

  const handleEnable = async () => {
    setBusy(true);
    try {
      await requestAndBindWebPushSubscription();
    } catch {
      // Permission denial or unavailable Push Service is reflected by the
      // browser APIs below; the in-app preference toggle remains authoritative.
    } finally {
      setPermission(getWebNotificationPermission());
      setPushState(getWebPushSupportState());
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
