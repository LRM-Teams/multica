import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const knowledgeKeys = {
  all: (wsId: string) => ["knowledge", wsId] as const,
  list: (wsId: string, kind: string) => [...knowledgeKeys.all(wsId), "list", kind] as const,
  item: (wsId: string, itemId: string) => [...knowledgeKeys.all(wsId), "item", itemId] as const,
  neighbors: (wsId: string, itemId: string, hops: number) =>
    [...knowledgeKeys.all(wsId), "neighbors", itemId, hops] as const,
};

/** Wiki page kinds exposed in the member product surface (LRM-1001). */
export type WikiUiKind = "context" | "decision" | "goal";

export function knowledgeListOptions(wsId: string, kind?: WikiUiKind | "") {
  const kindKey = kind ?? "";
  return queryOptions({
    queryKey: knowledgeKeys.list(wsId, kindKey),
    queryFn: () =>
      api.listTeamKnowledgeItems(wsId, {
        kind: kindKey || undefined,
        limit: 200,
        include_content: false,
      }),
    enabled: !!wsId,
  });
}

export function knowledgeItemOptions(wsId: string, itemId: string) {
  return queryOptions({
    queryKey: knowledgeKeys.item(wsId, itemId),
    queryFn: () => api.getTeamKnowledgeItem(wsId, itemId),
    enabled: !!wsId && !!itemId,
    retry: (count, err) => {
      const status = typeof err === "object" && err && "status" in err ? Number((err as { status: number }).status) : 0;
      if (status === 404 || status === 403) return false;
      return count < 2;
    },
  });
}

export function knowledgeNeighborsOptions(wsId: string, itemId: string, hops: 1 | 2 = 1) {
  return queryOptions({
    queryKey: knowledgeKeys.neighbors(wsId, itemId, hops),
    queryFn: () => api.listKnowledgeNeighbors(wsId, itemId, hops),
    enabled: !!wsId && !!itemId,
  });
}
