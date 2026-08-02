import { QueryClient } from "@tanstack/react-query";
import { installQueryOnlineRecovery } from "./query-online";

export function createQueryClient(): QueryClient {
  // LRM-844: install once per browser session so a missed `online` after a
  // cold-start `offline` cannot leave first-paint queries paused forever.
  installQueryOnlineRecovery();

  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: Infinity,
        gcTime: 10 * 60 * 1000, // 10 minutes
        refetchOnWindowFocus: false,
        refetchOnReconnect: true,
        // Explicit (not the library default) so "nobody chose this" can't be
        // mistaken for "we chose this". Pinned to the library's own value, so
        // this line changes nothing today — it only records the decision.
        //
        // ⚠️ On its own this option is nearly INERT here. query-core 5.96.2
        // (queryObserver.js:450) resolves it as
        //   value === "always" || (value !== false && isStale(query, options))
        // and our `staleTime: Infinity` means nothing is ever stale. So `true`
        // yields:
        //   · first mount, NO cached data → fetches (via shouldLoadOnMount)
        //   · remount WITH cached data    → does NOT refetch
        // The user-visible behaviour is therefore the PRODUCT of
        // refetchOnMount × staleTime, never this line by itself: changing
        // `staleTime` alone flips it while this value sits untouched. The guard
        // in query-client.test.ts asserts that product behaviourally for exactly
        // that reason — pinning only this option would be guard theatre.
        refetchOnMount: true,
        retry: 1,
        // Explicit (not the library default) so it's visible and guarded.
        // "online": offline reads don't fetch — they show the last cached data
        // rather than erroring. (No offline read blanks a populated view.)
        networkMode: "online",
      },
      mutations: {
        retry: false,
        // #1276: pin this explicitly. React Query's default "online" PAUSES a
        // mutation while offline — it neither runs nor fails, then silently
        // resumes on reconnect. For our writes (send / pin / rename / archive /
        // remove member …) that means "the user thought it didn't happen, but
        // it fires ten minutes later" — worse than a send, since archive/remove
        // aren't undoable. "always" makes every write ATTEMPT offline, fail fast
        // (restore + retry), and never silently auto-execute. No mutation here
        // relies on offline-pause (there is no outbox/offline queue), so this is
        // a safe global policy; a future queue-backed mutation can override to
        // "online" explicitly.
        networkMode: "always",
      },
    },
  });
}
