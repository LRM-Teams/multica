"use client";

import type { ReactNode } from "react";
import { SidebarProvider, SidebarInset } from "@multica/ui/components/ui/sidebar";
import { ModalRegistry } from "../modals/registry";
import { SourceBackfillModal } from "../onboarding";
import { AppSidebar } from "./app-sidebar";
import { DashboardGuard } from "./dashboard-guard";
import { NavigationProgress } from "./navigation-progress";
import { WorkspacePresencePrefetch } from "./workspace-presence-prefetch";
import { GlobalAgentPanel } from "./global-agent-panel";
import { GlobalMemberPanel } from "./global-member-panel";
import { AgentMemoryXpListener } from "../agents/components/agent-memory-xp-listener";
import { AgentHonorUnlockListener } from "../agents/components/agent-honor-unlock-listener";

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
        <AgentMemoryXpListener />
        <AgentHonorUnlockListener />
        <AppSidebar />
        <SidebarInset className="relative overflow-hidden">
          <NavigationProgress />
          {banner}
          <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
            {children}
          </div>
          <ModalRegistry />
          <SourceBackfillModal />
          <GlobalAgentPanel />
          <GlobalMemberPanel />
          {extra}
        </SidebarInset>
      </SidebarProvider>
    </DashboardGuard>
  );
}
