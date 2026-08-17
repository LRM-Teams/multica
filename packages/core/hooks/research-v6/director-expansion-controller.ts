import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import type {
  ResearchV6DirectorProjectionSnapshot,
  ResearchV6DirectorProjectionTransport,
} from "../../types/research-v6-director";
import { useResearchV6DirectorDisplayStore } from "./director-display-store";
import { researchV6DirectorProjectionKeys } from "./director-queries";

type SlicePages = InfiniteData<ResearchV6DirectorProjectionSnapshot, string | null>;

export class ResearchV6DirectorExpansionController {
  private requestSequence = 0;
  private controllers = new Map<string, AbortController>();

  constructor(
    private readonly queryClient: QueryClient,
    private readonly transport: ResearchV6DirectorProjectionTransport,
    private readonly identity: {
      workspaceId: string;
      runId: string;
      snapshotId: string;
    },
  ) {
    useResearchV6DirectorDisplayStore
      .getState()
      .setProjectionIdentity(
        identity.workspaceId,
        identity.runId,
        identity.snapshotId,
      );
  }

  async toggle(rootNodeId: string, failureMessage: string): Promise<void> {
    if (
      useResearchV6DirectorDisplayStore.getState().expandedByRoot[rootNodeId]
    ) {
      this.collapse(rootNodeId);
      return;
    }
    await this.load(rootNodeId, false, failureMessage);
  }

  async loadMore(rootNodeId: string, failureMessage: string): Promise<void> {
    await this.load(rootNodeId, true, failureMessage);
  }

  collapse(rootNodeId: string): void {
    this.controllers.get(rootNodeId)?.abort();
    this.controllers.delete(rootNodeId);
    useResearchV6DirectorDisplayStore.getState().collapseRoot(rootNodeId);
  }

  invalidateSliceKeys(sliceKeys: readonly string[]): void {
    const state = useResearchV6DirectorDisplayStore.getState();
    const invalidated = new Set(sliceKeys);
    for (const [rootNodeId, expansion] of Object.entries(state.expandedByRoot)) {
      if (!invalidated.has(expansion.sliceKey)) continue;
      this.queryClient.removeQueries({ queryKey: this.key(rootNodeId), exact: true });
    }
    state.invalidateSliceKeys(sliceKeys);
  }

  dispose(): void {
    for (const controller of this.controllers.values()) controller.abort();
    this.controllers.clear();
  }

  private async load(
    rootNodeId: string,
    nextPage: boolean,
    failureMessage: string,
  ): Promise<void> {
    const key = this.key(rootNodeId);
    const cached = this.queryClient.getQueryData<SlicePages>(key);
    const previousPages = cached?.pages ?? [];
    const lastPage = previousPages.at(-1);
    if (nextPage && (!lastPage?.has_more || !lastPage.next_cursor)) return;

    this.controllers.get(rootNodeId)?.abort();
    const controller = new AbortController();
    this.controllers.set(rootNodeId, controller);
    const requestToken = `${this.identity.snapshotId}:${++this.requestSequence}`;
    useResearchV6DirectorDisplayStore
      .getState()
      .beginExpansion(rootNodeId, requestToken);

    try {
      let pages = previousPages;
      let newNodeIds: readonly string[];
      if (!nextPage && pages.length > 0) {
        newNodeIds = pages.flatMap((page) => page.nodes.map((node) => node.id));
      } else {
        const page = await this.transport.loadSlice(
          this.identity.workspaceId,
          this.identity.runId,
          {
            root: rootNodeId,
            depth: 1,
            snapshot_id: this.identity.snapshotId,
            cursor: nextPage ? lastPage?.next_cursor : undefined,
          },
          controller.signal,
        );
        if (lastPage && page.slice_key !== lastPage.slice_key) {
          throw new Error("Director V6 slice pagination changed slice identity");
        }
        pages = nextPage ? [...previousPages, page] : [page];
        newNodeIds = page.nodes.map((node) => node.id);
        this.queryClient.setQueryData<SlicePages>(key, {
          pages: [...pages],
          pageParams: pages.map((_, index) =>
            index === 0 ? null : (pages[index - 1]?.next_cursor ?? null),
          ),
        });
      }
      const revealedNodeIds = pages.flatMap((page) =>
        page.nodes.map((node) => node.id),
      );
      const sliceKey = pages[0]?.slice_key;
      if (!sliceKey) throw new Error("Director V6 slice response was empty");
      useResearchV6DirectorDisplayStore
        .getState()
        .commitExpansion(
          rootNodeId,
          requestToken,
          sliceKey,
          revealedNodeIds,
          newNodeIds,
        );
    } catch (error) {
      if (error instanceof Error && error.name === "AbortError") return;
      useResearchV6DirectorDisplayStore
        .getState()
        .failExpansion(rootNodeId, requestToken, failureMessage);
    } finally {
      if (this.controllers.get(rootNodeId) === controller) {
        this.controllers.delete(rootNodeId);
      }
    }
  }

  private key(rootNodeId: string) {
    return researchV6DirectorProjectionKeys.slice(
      this.identity.workspaceId,
      this.identity.runId,
      this.identity.snapshotId,
      rootNodeId,
    );
  }
}
