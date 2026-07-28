import { describe, expect, it, vi } from "vitest";

// #1276 guard. The bug was NOT a code error — nobody wrote or deleted anything;
// React Query's DEFAULT networkMode "online" PAUSES a send while offline, so the
// fetch never runs and the failure path never fires (stuck "Sending…" + a silent
// resend on reconnect). `grep networkMode` was zero hits — invisible in our own
// source. We now set it "always". If a future refactor deletes that line as
// "redundant", the behavior silently reverts AND grep is zero again — and NOTHING
// goes red unless this test does.
//
// (A behaviour-level test — force offline via onlineManager, assert the mutation
// errors instead of pausing — was tried first but does NOT discriminate in jsdom:
// React Query's offline mutation-pause isn't reliably reproducible there, so it
// stayed green with "online" too = a false guard. This asserts the config value
// directly, which IS deterministic and goes red on revert.)
//
// Verified red-on-revert: with `networkMode: "online"` (or the line removed) both
// cases below FAIL; with "always" they pass.

const useMutationSpy = vi.fn((opts: unknown) => opts);
vi.mock("@tanstack/react-query", () => ({
  useMutation: (opts: unknown) => useMutationSpy(opts),
  useQueryClient: () => ({}),
}));
vi.mock("../hooks", () => ({ useWorkspaceId: () => "ws-1" }));

import {
  useSendChannelMessage,
  useSendChannelThreadMessage,
} from "./mutations";

describe("send mutations — #1276 networkMode 'always' guard", () => {
  it.each([
    ["useSendChannelMessage", useSendChannelMessage],
    ["useSendChannelThreadMessage", useSendChannelThreadMessage],
  ] as const)(
    "%s pins networkMode 'always' — an offline send must attempt+fail, never pause/silently-resend",
    (_name, hook) => {
      useMutationSpy.mockClear();
      hook();
      expect(useMutationSpy).toHaveBeenCalledTimes(1);
      expect(useMutationSpy.mock.calls[0]![0]).toMatchObject({
        networkMode: "always",
      });
    },
  );
});
