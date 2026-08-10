"use client";

import { lazy, Suspense, type ReactNode } from "react";
import { SidebarProvider, SidebarInset } from "@multica/ui/components/ui/sidebar";
import { AppSidebar } from "./app-sidebar";
import { DashboardGuard } from "./dashboard-guard";
import { NavigationProgress } from "./navigation-progress";
import { WorkspacePresencePrefetch } from "./workspace-presence-prefetch";

/**
 * LRM-1263 — keep the channel/home shell interactive without waiting on
 * profile panels, modal graphs, or honor/XP listeners. Those mount after the
 * shell paints (Suspense fallback null = no layout shift).
 */
const ModalRegistry = lazy(() =>
  import("../modals/registry").then((m) => ({ default: m.ModalRegistry })),
);
const SourceBackfillModal = lazy(() =>
  import("../onboarding").then((m) => ({ default: m.SourceBackfillModal })),
);
const GlobalAgentPanel = lazy(() =>
  import("./global-agent-panel").then((m) => ({ default: m.GlobalAgentPanel })),
);
const GlobalMemberPanel = lazy(() =>
  import("./global-member-panel").then((m) => ({
    default: m.GlobalMemberPanel,
  })),
);
const AgentMemoryXpListener = lazy(() =>
  import("../agents/components/agent-memory-xp-listener").then((m) => ({
    default: m.AgentMemoryXpListener,
  })),
);
const AgentHonorUnlockListener = lazy(() =>
  import("../agents/components/agent-honor-unlock-listener").then((m) => ({
    default: m.AgentHonorUnlockListener,
  })),
);
const ComputerUpdateToastListener = lazy(() =>
  import("../runtimes/components/computer-update-toast-listener").then((m) => ({
    default: m.ComputerUpdateToastListener,
  })),
);

interface DashboardLayoutProps {
  children: ReactNode;
  /** Rendered inside SidebarInset (e.g. ChatWindow, ChatFab — absolute-positioned overlays) */
  extra?: ReactNode;
  /**
   * In-flow banner above the main dashboard surface (e.g. Slack-style soft-ask).
   * Shrinks the content area instead of overlaying the channel header.
   */
  banner?: ReactNode;
  /** Loading indicator */
  loadingIndicator?: ReactNode;
}

export function DashboardLayout({
  children,
  extra,
  banner,
  loadingIndicator,
}: DashboardLayoutProps) {
  return (
    <DashboardGuard
      loadingFallback={
        <div className="flex h-svh items-center justify-center">
          {loadingIndicator}
        </div>
      }
    >
      <SidebarProvider className="h-svh">
        <WorkspacePresencePrefetch />
        <Suspense fallback={null}>
          <AgentMemoryXpListener />
          <AgentHonorUnlockListener />
          <ComputerUpdateToastListener />
        </Suspense>
        <AppSidebar />
        <SidebarInset className="relative overflow-hidden">
          <NavigationProgress />
          {banner}
          <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
            {children}
          </div>
          <Suspense fallback={null}>
            <ModalRegistry />
            <SourceBackfillModal />
            <GlobalAgentPanel />
            <GlobalMemberPanel />
          </Suspense>
          {extra}
        </SidebarInset>
      </SidebarProvider>
    </DashboardGuard>
  );
}
