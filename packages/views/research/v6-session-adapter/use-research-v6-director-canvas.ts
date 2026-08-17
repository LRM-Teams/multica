"use client";

import { useEffect, useMemo } from "react";
import {
  useInfiniteQuery,
  useQueryClient,
  type InfiniteData,
} from "@tanstack/react-query";
import {
  researchV6DirectorDisplayIdentity,
  useResearchV6DirectorDisplayStore,
} from "@multica/core/research-v6/director-display-store";
import { ResearchV6DirectorExpansionController } from "@multica/core/research-v6/director-expansion-controller";
import {
  researchV6DirectorProjectionKeys,
  researchV6DirectorSnapshotOptions,
} from "@multica/core/research-v6/director-queries";
import type {
  ResearchV6DirectorDensityBin,
  ResearchV6DirectorProjectionEdge,
  ResearchV6DirectorProjectionNode,
  ResearchV6DirectorProjectionSnapshot,
  ResearchV6DirectorProjectionTransport,
} from "@multica/core/types/research-v6-director";
import type { StarGraphExpansionControl } from "../star-graph/lib/star-graph-expansion";
import {
  adaptResearchV6DirectorCanvas,
  type ResearchV6DirectorCanvasAdapterResult,
} from "./director-session-adapter";

type SlicePages = InfiniteData<ResearchV6DirectorProjectionSnapshot, string | null>;

export interface UseResearchV6DirectorCanvasResult {
  canvas: ResearchV6DirectorCanvasAdapterResult | null;
  expansionControl: StarGraphExpansionControl | undefined;
  isLoading: boolean;
  isFetching: boolean;
  error: Error | null;
  hasNextSnapshotPage: boolean;
  loadNextSnapshotPage: () => void;
  loadMoreExpansion: (rootNodeId: string) => void;
}

export function useResearchV6DirectorCanvas({
  workspaceId,
  runId,
  transport,
  enabled = true,
  expansionFailureLabel,
  lowPerformance = false,
}: {
  workspaceId: string;
  runId: string;
  transport: ResearchV6DirectorProjectionTransport;
  enabled?: boolean;
  expansionFailureLabel: string;
  lowPerformance?: boolean;
}): UseResearchV6DirectorCanvasResult {
  const queryClient = useQueryClient();
  const snapshotQuery = useInfiniteQuery({
    ...researchV6DirectorSnapshotOptions(transport, workspaceId, runId),
    enabled,
  });
  const firstPage = snapshotQuery.data?.pages[0] ?? null;
  const snapshotId = firstPage?.snapshot_id ?? null;
  const expectedDisplayIdentity = firstPage
    ? researchV6DirectorDisplayIdentity(
        workspaceId,
        runId,
        firstPage.snapshot_id,
      )
    : null;
  const displayIdentity = useResearchV6DirectorDisplayStore(
    (state) => state.identity,
  );
  const expandedByRoot = useResearchV6DirectorDisplayStore(
    (state) => state.expandedByRoot,
  );
  const requestTokenByRoot = useResearchV6DirectorDisplayStore(
    (state) => state.requestTokenByRoot,
  );
  const failureByRoot = useResearchV6DirectorDisplayStore(
    (state) => state.failureByRoot,
  );
  const transition = useResearchV6DirectorDisplayStore(
    (state) => state.transition,
  );
  const displayMatches =
    expectedDisplayIdentity !== null && displayIdentity === expectedDisplayIdentity;

  const controller = useMemo(
    () =>
      snapshotId
        ? new ResearchV6DirectorExpansionController(queryClient, transport, {
            workspaceId,
            runId,
            snapshotId,
          })
        : null,
    [queryClient, runId, snapshotId, transport, workspaceId],
  );
  useEffect(() => () => controller?.dispose(), [controller]);

  const canvas = useMemo(() => {
    const defaultPages = snapshotQuery.data?.pages ?? [];
    if (!firstPage || defaultPages.length === 0) return null;
    const nodes: ResearchV6DirectorProjectionNode[] = [];
    const edges: ResearchV6DirectorProjectionEdge[] = [];
    const densityBins: ResearchV6DirectorDensityBin[] = [];
    for (const page of defaultPages) {
      nodes.push(...page.nodes);
      edges.push(...page.edges);
      densityBins.push(...page.density_bins);
    }
    if (displayMatches) {
      for (const rootNodeId of Object.keys(expandedByRoot)) {
        const pages = queryClient.getQueryData<SlicePages>(
          researchV6DirectorProjectionKeys.slice(
            workspaceId,
            runId,
            firstPage.snapshot_id,
            rootNodeId,
          ),
        );
        for (const page of pages?.pages ?? []) {
          nodes.push(...page.nodes);
          edges.push(...page.edges);
        }
      }
    }
    return adaptResearchV6DirectorCanvas({
      runId,
      eventSequence: firstPage.through_event_sequence,
      nodes: dedupeById(nodes),
      edges: dedupeById(edges),
      densityBins: dedupeById(densityBins),
    });
  }, [
    displayMatches,
    expandedByRoot,
    firstPage,
    queryClient,
    runId,
    snapshotQuery.data?.pages,
    workspaceId,
  ]);

  const expansionControl = useMemo<StarGraphExpansionControl | undefined>(() => {
    if (!canvas || !controller) return undefined;
    const expandedRoots = displayMatches ? Object.keys(expandedByRoot) : [];
    return {
      expandableNodeIds: canvas.expandableNodeIds,
      expandedNodeIds: new Set(expandedRoots),
      loadingNodeIds: new Set(
        displayMatches ? Object.keys(requestTokenByRoot) : [],
      ),
      failedNodeIds: new Set(
        displayMatches ? Object.keys(failureByRoot) : [],
      ),
      failureLabel: expansionFailureLabel,
      transition:
        displayMatches && transition
          ? {
              sequence: transition.sequence,
              kind: transition.kind,
              rootNodeId: transition.rootNodeId,
              revealedNodeIds: transition.revealedNodeIds,
            }
          : null,
      lowPerformance,
      onToggleNode: (nodeId) => {
        void controller.toggle(nodeId, expansionFailureLabel);
      },
    };
  }, [
    canvas,
    controller,
    displayMatches,
    expandedByRoot,
    expansionFailureLabel,
    failureByRoot,
    lowPerformance,
    requestTokenByRoot,
    transition,
  ]);

  return {
    canvas,
    expansionControl,
    isLoading: snapshotQuery.isLoading,
    isFetching: snapshotQuery.isFetching,
    error:
      snapshotQuery.error instanceof Error ? snapshotQuery.error : null,
    hasNextSnapshotPage: snapshotQuery.hasNextPage,
    loadNextSnapshotPage: () => {
      void snapshotQuery.fetchNextPage();
    },
    loadMoreExpansion: (rootNodeId) => {
      if (!controller) return;
      void controller.loadMore(rootNodeId, expansionFailureLabel);
    },
  };
}

function dedupeById<T extends { id: string }>(items: readonly T[]): T[] {
  const byId = new Map<string, T>();
  for (const item of items) byId.set(item.id, item);
  return [...byId.values()];
}
