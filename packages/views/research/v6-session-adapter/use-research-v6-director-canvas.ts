"use client";

import { useEffect, useMemo, useSyncExternalStore } from "react";
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
  ResearchV6DirectorLiveController,
  type ResearchV6DirectorRealtimeBus,
} from "@multica/core/research-v6-live/director-controller";
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
  snapshotId: string | null;
  expansionControl: StarGraphExpansionControl | undefined;
  isLoading: boolean;
  isFetching: boolean;
  error: Error | null;
  hasNextSnapshotPage: boolean;
  loadNextSnapshotPage: () => void;
  loadMoreExpansion: (rootNodeId: string) => void;
  refetch: () => void;
}

export function useResearchV6DirectorCanvas({
  workspaceId,
  runId,
  transport,
  enabled = true,
  expansionFailureLabel,
  lowPerformance = false,
  realtimeBus,
}: {
  workspaceId: string;
  runId: string;
  transport: ResearchV6DirectorProjectionTransport;
  enabled?: boolean;
  expansionFailureLabel: string;
  lowPerformance?: boolean;
  realtimeBus?: ResearchV6DirectorRealtimeBus;
}): UseResearchV6DirectorCanvasResult {
  const queryClient = useQueryClient();
  const snapshotQuery = useInfiniteQuery({
    ...researchV6DirectorSnapshotOptions(transport, workspaceId, runId),
    enabled,
  });
  // React Query can retain the previous successful page while a refetch is
  // failing. Never render that cached projection after an identity/schema
  // failure: the transport is fail-closed and the canvas must resync first.
  const firstPage = snapshotQuery.error
    ? null
    : snapshotQuery.data?.pages[0] ?? null;
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

  const liveController = useMemo(
    () =>
      snapshotId && realtimeBus
        ? new ResearchV6DirectorLiveController(
            { workspaceId, runId },
            transport,
            realtimeBus,
            {
              onInvalidateSliceKeys: (sliceKeys) => {
                controller?.invalidateSliceKeys(sliceKeys);
                if (firstPage && sliceKeys.includes(firstPage.slice_key)) {
                  void queryClient.invalidateQueries({
                    queryKey: researchV6DirectorProjectionKeys.snapshot(
                      workspaceId,
                      runId,
                    ),
                  });
                }
              },
            },
          )
        : null,
    [
      controller,
      firstPage,
      queryClient,
      realtimeBus,
      runId,
      snapshotId,
      transport,
      workspaceId,
    ],
  );
  useEffect(() => {
    if (!liveController) return;
    for (const page of snapshotQuery.data?.pages ?? []) {
      liveController.seedSnapshotPage(page);
    }
  }, [liveController, snapshotQuery.data?.pages]);
  useEffect(() => {
    if (!liveController) return;
    liveController.connect();
    return () => liveController.disconnect();
  }, [liveController]);
  const liveRevision = useSyncExternalStore(
    liveController
      ? (listener) => liveController.subscribe(listener)
      : EMPTY_SUBSCRIBE,
    liveController ? () => liveController.getRevision() : ZERO_SNAPSHOT,
    ZERO_SNAPSHOT,
  );

  const canvas = useMemo(() => {
    if (snapshotQuery.error) return null;
    const defaultPages = snapshotQuery.data?.pages ?? [];
    if (!firstPage || defaultPages.length === 0) return null;
    const liveView = liveController
      ?.getClient()
      .getState()
      .views.get(firstPage.slice_key);
    // Keep absorbed inputs long enough for the adapter to project their
    // immutable assimilation lineage onto the visible successor. The adapter
    // owns the final default-visibility filter.
    const nodes: ResearchV6DirectorProjectionNode[] = liveView
      ? [...liveView.nodes.values()]
      : defaultPages.flatMap((page) => page.nodes);
    const edges: ResearchV6DirectorProjectionEdge[] = liveView
      ? [...liveView.edges.values()]
      : defaultPages.flatMap((page) => page.edges);
    const densityBins: ResearchV6DirectorDensityBin[] = liveView
      ? [...liveView.densityBins.values()]
      : defaultPages.flatMap((page) => page.density_bins);
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
      eventSequence:
        liveController?.getClient().getState().lastConfirmedSequence ??
        firstPage.through_event_sequence,
      nodes: dedupeById(nodes),
      edges: dedupeById(edges),
      densityBins: dedupeById(densityBins),
    });
  }, [
    displayMatches,
    expandedByRoot,
    firstPage,
    liveController,
    liveRevision,
    queryClient,
    runId,
    snapshotQuery.data?.pages,
    snapshotQuery.error,
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
    snapshotId:
      liveController?.getClient().getState().snapshotId ?? snapshotId,
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
    refetch: () => {
      void snapshotQuery.refetch();
    },
  };
}

function dedupeById<T extends { id: string }>(items: readonly T[]): T[] {
  const byId = new Map<string, T>();
  for (const item of items) byId.set(item.id, item);
  return [...byId.values()];
}

const EMPTY_SUBSCRIBE = () => () => {};
const ZERO_SNAPSHOT = () => 0;
