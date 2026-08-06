import { describe, expect, it } from "vitest";
import { QueryObserver } from "@tanstack/react-query";
import { createQueryClient } from "./query-client";

// #1276 guard (global). React Query's default networkMode is "online", which
// PAUSES a mutation while offline — it neither runs nor fails, then silently
// resumes on reconnect. For our writes that means "the user thought it didn't
// happen, but it fired later" (see query-client.ts). These assertions pin the
// explicit values so a silent revert to the library default is caught.
// Verified red-on-revert: flipping mutations→"online" (or deleting the line)
// fails the first test; flipping queries fails the second.
describe("createQueryClient — networkMode defaults (#1276)", () => {
  it("pins mutations.networkMode to 'always' — offline writes attempt+fail-fast, never silent-pause/resend", () => {
    const defaults = createQueryClient().getDefaultOptions();
    expect(defaults.mutations?.networkMode).toBe("always");
  });

  it("pins queries.networkMode to 'online' explicitly — no silent library default", () => {
    const defaults = createQueryClient().getDefaultOptions();
    expect(defaults.queries?.networkMode).toBe("online");
  });

  it("installs LRM-844 online recovery as a side effect of createQueryClient", async () => {
    // jsdom: createQueryClient must leave onlineManager online even if something
    // latched false before the client was built.
    const { onlineManager } = await import("@tanstack/react-query");
    const { resetQueryOnlineRecoveryForTests } = await import("./query-online");
    resetQueryOnlineRecoveryForTests();
    onlineManager.setOnline(false);
    createQueryClient();
    expect(onlineManager.isOnline()).toBe(true);
  });
});

// #835 audit guard. `refetchOnMount` was never set, so it fell through to the
// library default `true` — and `true` only refetches STALE data, while our
// `staleTime: Infinity` means nothing is ever stale. Neither option is wrong
// alone; the behaviour is their PRODUCT, which is why `grep refetchOnMount`
// found nothing while the effect was real. Pinning it records the decision
// without changing behaviour.
//
// The first two tests pin the values. The third is the one that matters: it
// asserts the BEHAVIOUR the pair produces, because a value-only guard is
// defeated by changing the *other* half — someone could set `staleTime: 0`,
// flip every screen to refetching on mount, and leave both value assertions
// green.
//
// Verified red-on-revert (each flipped alone, rest untouched):
//   · delete `refetchOnMount: true`      → test 1 RED (undefined), test 3 green
//   · `refetchOnMount: false`            → test 1 RED,             test 3 green
//   · `staleTime: 0` (refetchOnMount kept) → test 2 RED and test 3 RED
// That last row is the point: only the behavioural test moves when the half
// nobody is looking at changes.
describe("createQueryClient — refetchOnMount × staleTime (#835 audit)", () => {
  it("pins queries.refetchOnMount explicitly — 'unset' must not read as 'chosen'", () => {
    const defaults = createQueryClient().getDefaultOptions();
    expect(defaults.queries?.refetchOnMount).toBe(true);
  });

  it("pins queries.staleTime to Infinity — the other half of the product", () => {
    const defaults = createQueryClient().getDefaultOptions();
    expect(defaults.queries?.staleTime).toBe(Infinity);
  });

  it("remounting a query that already has cached data does NOT refetch — the behaviour the pair produces", async () => {
    const queryClient = createQueryClient();
    const queryKey = ["guard", "refetch-on-mount"];
    let fetchCount = 0;
    const queryFn = async (): Promise<string> => {
      fetchCount += 1;
      return "cached-value";
    };

    // Populate the cache the way a first visit would.
    await queryClient.fetchQuery({ queryKey, queryFn });
    expect(fetchCount).toBe(1);

    // A second mount of the same key — this is the moment `refetchOnMount`
    // governs. Subscribing is what triggers the observer's mount path.
    const observer = new QueryObserver(queryClient, { queryKey, queryFn });
    const unsubscribe = observer.subscribe(() => {});
    await new Promise((resolve) => setTimeout(resolve, 0));
    unsubscribe();

    // Still 1: cached data is served as-is. If this ever reads 2, a screen that
    // used to render instantly from cache now round-trips on every mount.
    expect(fetchCount).toBe(1);
  });
});
