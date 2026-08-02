"use client";

import { useCallback, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  researchKeys,
  researchSessionListOptions,
} from "@multica/core/research";
import { useWorkspaceId } from "@multica/core/hooks";
import { cn } from "@multica/ui/lib/utils";
import { useBrowserOnline } from "../lib/use-browser-online";
import { useResearchReconnect } from "../lib/use-research-reconnect";
import { resolveOfflineBannerMode } from "../lib/network-status";
import { ResearchOfflineBanner } from "./research-offline-banner";

/**
 * LRM-833 — wraps research list/session so offline shows a top banner (content
 * stays mounted) and coming back online auto-refetches with toast + manual retry.
 */
export function ResearchConnectivityShell({
  children,
  className,
  onReconnect,
}: {
  children: ReactNode;
  className?: string;
  /** Override default research-query invalidate (tests / scoped refetch). */
  onReconnect?: () => Promise<unknown>;
}) {
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const online = useBrowserOnline();

  const defaultReconnect = useCallback(async () => {
    await qc.invalidateQueries({ queryKey: researchKeys.all(wsId) });
    // fetchQuery throws on 5xx / network failure so reconnect phase can fail.
    await qc.fetchQuery({
      ...researchSessionListOptions(wsId),
      staleTime: 0,
    });
  }, [qc, wsId]);

  const reconnect = onReconnect ?? defaultReconnect;
  const { phase, retry } = useResearchReconnect({ online, reconnect });
  const bannerMode = resolveOfflineBannerMode(online, phase);

  return (
    <div
      className={cn("flex h-full min-h-0 flex-col", className)}
      data-testid="research-connectivity-shell"
      data-online={online ? "true" : "false"}
      data-reconnect-phase={phase}
    >
      {bannerMode ? (
        <ResearchOfflineBanner
          mode={bannerMode}
          onRetry={bannerMode === "offline" ? undefined : retry}
        />
      ) : null}
      <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
        {children}
      </div>
    </div>
  );
}
