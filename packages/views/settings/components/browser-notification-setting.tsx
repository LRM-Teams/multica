"use client";

import { useState, useSyncExternalStore } from "react";
import { api, ApiError } from "@multica/core/api";
import {
  getWebNotificationPermission,
  isWebNotificationSupported,
  type WebNotificationPermission,
} from "@multica/core/platform";
import {
  bindCurrentWebPushSubscription,
  getWebPushSupportState,
  requestAndBindWebPushSubscription,
} from "@multica/core/web-push";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { toast } from "sonner";
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
  const [testing, setTesting] = useState(false);
  // Durable failure copy — toast alone auto-dismisses / can be evicted (#835).
  const [testError, setTestError] = useState<string | null>(null);
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

  const handleSendTest = async () => {
    if (permission !== "granted" || pushState !== "supported") return;
    setTesting(true);
    setTestError(null);
    try {
      // Ensure this device has a bound subscription before asking the server
      // to fan out — otherwise a stale "Enabled" badge looks like a false pass.
      await bindCurrentWebPushSubscription();
      await api.sendTestWebPush();
      toast.success(t(($) => $.notifications.browser.test.success));
    } catch (err) {
      const message =
        err instanceof ApiError && err.message
          ? err.message
          : err instanceof Error && err.message
            ? err.message
            : t(($) => $.notifications.browser.test.failed_generic);
      setTestError(message);
      showErrorToast(message);
      console.error("[web-push] send test failed", err);
    } finally {
      setTesting(false);
      refresh((version) => version + 1);
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

  const canTest = pushState === "supported";
  const testEnabled = canTest && permission === "granted" && !testing && !busy;

  return (
    <Card>
      <CardContent>
        <div className="flex items-center justify-between gap-4">
          <div className="space-y-0.5 pr-4">
            <p className="text-sm font-medium">
              {t(($) => $.notifications.browser.label)}
            </p>
            <p className="text-xs text-muted-foreground">{statusHint}</p>
            {canTest && permission !== "granted" && (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.notifications.browser.test.need_permission)}
              </p>
            )}
            {testError && (
              <p className="text-xs text-destructive" role="alert" data-testid="browser-notification-test-error">
                {testError}
              </p>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {permission !== "granted" && pushState === "supported" && (
              <Button size="sm" variant="outline" onClick={handleEnable} disabled={busy || testing}>
                {t(($) => $.notifications.browser.enable)}
              </Button>
            )}
            {permission === "granted" && (
              <span className="text-xs font-medium text-muted-foreground">
                {t(($) => $.notifications.browser.enabled_badge)}
              </span>
            )}
            {canTest && (
              <Button
                size="sm"
                variant="outline"
                data-testid="browser-notification-send-test"
                onClick={() => void handleSendTest()}
                disabled={!testEnabled}
              >
                {testing
                  ? t(($) => $.notifications.browser.test.sending)
                  : t(($) => $.notifications.browser.test.send)}
              </Button>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
