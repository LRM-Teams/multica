/** Harness stub — create/find succeeds without hitting the API. */
export function useOpenDM() {
  return {
    openDM: async () => ({ id: "dm-harness-1" }),
    isPending: false,
  };
}
