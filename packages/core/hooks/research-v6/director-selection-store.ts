import { create } from "zustand";
import type {
  ResearchV6DirectorProjectionNode,
  ResearchV6DirectorSelectedRef,
} from "../../types/research-v6-director";

export function researchV6DirectorSelectedRefFromNode(
  node: ResearchV6DirectorProjectionNode,
): ResearchV6DirectorSelectedRef | null {
  const { canonical_ref: ref } = node;
  if (!ref.revision || !ref.content_hash) return null;
  return {
    stable_id: `${ref.kind}:${ref.id}`,
    kind: ref.kind,
    entity_id: ref.id,
    revision: ref.revision,
    content_hash: ref.content_hash,
    display_summary: node.catalog_summary,
  };
}

interface ResearchV6DirectorSelectionState {
  byProjection: Record<string, ResearchV6DirectorSelectedRef | null>;
  select: (
    workspaceId: string,
    runId: string,
    ref: ResearchV6DirectorSelectedRef,
  ) => void;
  clear: (workspaceId: string, runId: string) => void;
}

export function researchV6DirectorSelectionIdentity(
  workspaceId: string,
  runId: string,
): string {
  return `${workspaceId}:${runId}`;
}

/** Client-only inspector/composer selection. It never owns node lifecycle data. */
export const useResearchV6DirectorSelectionStore =
  create<ResearchV6DirectorSelectionState>()((set) => ({
    byProjection: {},
    select: (workspaceId, runId, ref) =>
      set((state) => ({
        byProjection: {
          ...state.byProjection,
          [researchV6DirectorSelectionIdentity(workspaceId, runId)]: ref,
        },
      })),
    clear: (workspaceId, runId) =>
      set((state) => {
        const byProjection = { ...state.byProjection };
        delete byProjection[
          researchV6DirectorSelectionIdentity(workspaceId, runId)
        ];
        return { byProjection };
      }),
  }));
