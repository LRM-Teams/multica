import { queryOptions } from "@tanstack/react-query";
import { api } from "@/data/api";

export const memberListOptions = (wsId: string | null) =>
  queryOptions({
    queryKey: ["members", wsId] as const,
    queryFn: ({ signal }) => api.listMembers(wsId!, { signal }),
    enabled: !!wsId,
  });

/** LRM-391: identity for actors omitted from ListAgents / members directory. */
export const memberProfileOptions = (
  wsId: string | null,
  memberType: "user" | "agent" | null | undefined,
  memberId: string | null | undefined,
) => {
  const type = memberType ?? "user";
  const id = memberId ?? "";
  return queryOptions({
    queryKey: ["member-profiles", wsId, type, id] as const,
    queryFn: ({ signal }) => api.getMemberProfile(type, id, { signal }),
    enabled: !!wsId && !!memberType && !!memberId,
    staleTime: 30 * 1000,
  });
};
