"use client";

import { DashboardLayout } from "@multica/views/layout";
import { MulticaIcon } from "@multica/ui/components/common/multica-icon";
import { GlobalSearchDialog } from "@multica/views/search";
import { WebNotificationBridge } from "@/components/web-notification-bridge";
import { BrowserNotificationPrompt } from "@multica/views/settings/browser-notification-prompt";

export default function Layout({ children }: { children: React.ReactNode }) {
  return (
    <DashboardLayout
      loadingIndicator={<MulticaIcon className="size-6" />}
      banner={<BrowserNotificationPrompt />}
      extra={
        <>
          <GlobalSearchDialog />
          <WebNotificationBridge />
        </>
      }
    >
      {children}
    </DashboardLayout>
  );
}
