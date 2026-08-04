"use client";

import { lazy, Suspense } from "react";
import { DashboardLayout } from "@multica/views/layout";
import { MulticaIcon } from "@multica/ui/components/common/multica-icon";

/** LRM-1263 — search / notification chrome is not on the channel-shell critical path. */
const GlobalSearchDialog = lazy(() =>
  import("@multica/views/search").then((m) => ({
    default: m.GlobalSearchDialog,
  })),
);
const WebNotificationBridge = lazy(() =>
  import("@/components/web-notification-bridge").then((m) => ({
    default: m.WebNotificationBridge,
  })),
);
const BrowserNotificationPrompt = lazy(() =>
  import("@multica/views/settings/browser-notification-prompt").then((m) => ({
    default: m.BrowserNotificationPrompt,
  })),
);

export default function Layout({ children }: { children: React.ReactNode }) {
  return (
    <DashboardLayout
      loadingIndicator={<MulticaIcon className="size-6" />}
      banner={
        <Suspense fallback={null}>
          <BrowserNotificationPrompt />
        </Suspense>
      }
      extra={
        <Suspense fallback={null}>
          <GlobalSearchDialog />
          <WebNotificationBridge />
        </Suspense>
      }
    >
      {children}
    </DashboardLayout>
  );
}
