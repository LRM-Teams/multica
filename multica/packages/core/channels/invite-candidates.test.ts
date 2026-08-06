import { beforeEach, describe, expect, it, vi } from "vitest";

const listChannelInviteCandidatesMock = vi.fn().mockResolvedValue({
  candidates: [
    {
      member_type: "user",
      member_id: "u1",
      name: "alice",
      display_name: "Alice",
      email: "a@example.com",
      avatar_url: null,
    },
  ],
});

vi.mock("../api", () => ({
  api: {
    listChannelInviteCandidates: (...args: unknown[]) =>
      listChannelInviteCandidatesMock(...args),
  },
}));

import { channelInviteCandidatesOptions, channelKeys } from "./queries";

describe("channelInviteCandidatesOptions (LRM-622)", () => {
  beforeEach(() => listChannelInviteCandidatesMock.mockClear());

  it("uses channelKeys.inviteCandidates and returns candidates array", async () => {
    const opts = channelInviteCandidatesOptions("chan-1");
    expect(opts.queryKey).toEqual(channelKeys.inviteCandidates("chan-1"));
    expect(opts.enabled).toBe(true);
    const data = await opts.queryFn!({} as never);
    expect(listChannelInviteCandidatesMock).toHaveBeenCalledWith("chan-1");
    expect(data).toEqual([
      {
        member_type: "user",
        member_id: "u1",
        name: "alice",
        display_name: "Alice",
        email: "a@example.com",
        avatar_url: null,
      },
    ]);
  });

  it("disables when channelId is empty", () => {
    expect(channelInviteCandidatesOptions("").enabled).toBe(false);
  });
});
