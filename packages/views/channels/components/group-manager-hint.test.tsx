import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { GroupManagerHint } from "./group-manager-hint";

type Member = {
  member_type: "user" | "agent";
  member_id: string;
  name: string;
  display_name: string;
  avatar_url: string | null;
  role?: string;
};

const membersMock = vi.hoisted(() => ({ current: [] as Member[] }));

vi.mock("@multica/core/channels", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/channels")>()),
  channelMembersOptions: () => ({
    queryKey: ["channel-members"],
    queryFn: async () => membersMock.current,
  }),
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "me" } }),
}));

function member(id: string, role: string | undefined, type: "user" | "agent" = "user"): Member {
  return {
    member_type: type,
    member_id: id,
    name: id,
    display_name: id,
    avatar_url: null,
    role,
  };
}

function renderHint(onOpenMembers = vi.fn()) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={qc}>
        <GroupManagerHint channelId="chan-1" onOpenMembers={onOpenMembers} />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return onOpenMembers;
}

describe("GroupManagerHint (#808 — owner, zero managers)", () => {
  beforeEach(() => {
    window.localStorage.clear();
    membersMock.current = [];
  });

  it("shows for the owner when the group has no manager", async () => {
    membersMock.current = [member("me", "owner"), member("bob", "member")];
    renderHint();
    expect(await screen.findByTestId("group-manager-hint")).toBeInTheDocument();
    expect(screen.getByText("No group manager yet")).toBeInTheDocument();
  });

  it("hides once the group has a manager", async () => {
    membersMock.current = [
      member("me", "owner"),
      member("agent-1", "manager", "agent"),
    ];
    renderHint();
    // Give the query a tick to settle, then assert it never appears.
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByTestId("group-manager-hint")).toBeNull();
  });

  it("hides for a non-owner viewer (member / manager)", async () => {
    membersMock.current = [member("me", "member"), member("alice", "owner")];
    renderHint();
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByTestId("group-manager-hint")).toBeNull();
  });

  it("fails closed when the server omits role — no hint from unknown data", async () => {
    // `channelMemberRole` defaults a missing role to "member", so an absent
    // `channel_member.role` must NOT be read as "owner with no manager".
    membersMock.current = [member("me", undefined), member("bob", undefined)];
    renderHint();
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByTestId("group-manager-hint")).toBeNull();
  });

  it("CTA only navigates to members — it never assigns a manager", async () => {
    const user = userEvent.setup();
    membersMock.current = [member("me", "owner")];
    const onOpenMembers = renderHint();
    await screen.findByTestId("group-manager-hint");
    await user.click(screen.getByTestId("group-manager-hint-cta"));
    expect(onOpenMembers).toHaveBeenCalledTimes(1);
    // Still visible: navigating is not "designated".
    expect(screen.getByTestId("group-manager-hint")).toBeInTheDocument();
  });

  it("stays dismissed for that channel after the user closes it", async () => {
    const user = userEvent.setup();
    membersMock.current = [member("me", "owner")];
    renderHint();
    await screen.findByTestId("group-manager-hint");
    await user.click(screen.getByTestId("group-manager-hint-dismiss"));
    expect(screen.queryByTestId("group-manager-hint")).toBeNull();

    // A fresh mount (e.g. reopening the panel) honours the dismissal.
    renderHint();
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByTestId("group-manager-hint")).toBeNull();
  });
});
