/**
 * LRM-1348 harness stub — keeps every real query option factory (other modules
 * in the tree import several of them) and only neutralises the member-profile
 * fetch, which would otherwise hit a backend the harness does not run.
 */
export * from "../../../packages/core/workspace/queries";

export function memberProfileOptions(_wsId: string, type: string, id: string) {
  return {
    queryKey: ["harness", "member-profiles", type, id],
    queryFn: async () => {
      throw new Error(`harness: no profile for ${type}/${id}`);
    },
    enabled: false,
    retry: false,
  };
}
