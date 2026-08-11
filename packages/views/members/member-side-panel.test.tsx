import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MemberSidePanel } from "./member-side-panel";

const memberListMock = vi.fn();
const profileMock = vi.fn();
const agentsMock = vi.fn();
const runCountsMock = vi.fn();

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQuery: (options: { queryKey?: unknown[] }) => {
      const key = options.queryKey ?? [];
      if (key.includes("members") && !String(key).includes("member-profiles")) {
        return memberListMock();
      }
      if (key.includes("member-profiles")) {
        return profileMock();
      }
      if (key.includes("agents")) {
        return agentsMock();
      }
      if (String(key).includes("run-counts") || key.includes("runCounts")) {
        return runCountsMock();
      }
      return { data: undefined, isPending: false };
    },
  };
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } | null; setUser: () => void }) => unknown) =>
    sel({ user: { id: "u-self" }, setUser: vi.fn() }),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/agents/${id}`,
    actorProfile: (type: string, id: string) => `/profile/${type}/${id}`,
    settings: () => "/ws-1/settings",
  }),
}));

vi.mock("../navigation", () => ({
  AppLink: ({ children, href }: { children: React.ReactNode; href: string }) => (
    <a href={href}>{children}</a>
  ),
}));

vi.mock("../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <div data-testid={`avatar-${actorId}`} />
  ),
}));

vi.mock("../common/agent-panel-context", () => ({
  useOpenAgentPanel: () => null,
}));

vi.mock("../common/use-open-dm", () => ({
  useOpenDM: () => ({ openDM: vi.fn(), isPending: false }),
}));

vi.mock("../agents/components/inline-field-editor", () => ({
  InlineFieldEditor: ({
    value,
    emptyLabel,
    displayContent,
    testId = "inline-field-editor",
  }: {
    value: string;
    emptyLabel?: string;
    displayContent?: React.ReactNode;
    testId?: string;
  }) => (
    <div data-testid={`${testId}-trigger`}>
      {value ? (displayContent ?? value) : emptyLabel}
    </div>
  ),
}));

vi.mock("../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (keys: Record<string, unknown>) => string) =>
      fn({
        card: { unavailable: "unavailable" },
        role: { owner: "Owner", admin: "Admin", member: "Member" },
        panel: {
          description: "Description",
          no_description: "No description",
          description_placeholder: "Add…",
          display_name_label: "Display name",
          name_label: "Name",
          name_placeholder: "Name",
          name_required: "required",
          info: "Info",
          role: "Role",
          email: "Email",
          joined: "Joined",
          actions_section: "Actions",
          message_button: "Message",
          change_role_aria: "Change role",
          role_dialog_subtitle: "Pick a role",
          created_agents: "Created agents",
          no_agents: "No agents created yet",
          message_aria: "Send message",
          you_suffix: "(you)",
          edit_profile: "Edit profile",
          change_avatar_aria: "Change avatar",
          avatar_updated_toast: "Avatar updated",
          avatar_upload_failed: "Avatar upload failed",
          avatar_err_type: "PNG/JPG only",
          avatar_err_size: "Too large",
          avatar_err_dimensions: "Too small",
        },
        profile_popover: {
          close_aria: "Close",
          honor: { level_value: "Level {{level}}" },
        },
        side_panel: { back_to_messages: "Back to messages" },
      } as never),
  }),
}));

function renderPanel(userId: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemberSidePanel userId={userId} onClose={vi.fn()} onMessage={vi.fn()} />
    </QueryClientProvider>,
  );
}

describe("MemberSidePanel (LRM-619 lock A)", () => {
  beforeEach(() => {
    memberListMock.mockReturnValue({
      data: [
        {
          id: "m1",
          workspace_id: "ws-1",
          user_id: "u-frank",
          role: "owner",
          created_at: "2026-06-11T00:00:00Z",
          name: "frank-an",
          display_name: "Frank An",
          email: "me@example.com",
          avatar_url: null,
          profile_description: "",
        },
      ],
      isPending: false,
    });
    profileMock.mockReturnValue({
      data: {
        member_type: "user",
        member_id: "u-frank",
        name: "frank-an",
        display_name: "Frank An",
        avatar_url: null,
        description: "",
        role: "owner",
        status: null,
        recent_activity: [],
        profile_access: "full",
      },
      isPending: false,
    });
    agentsMock.mockReturnValue({
      data: [
        {
          id: "a1",
          owner_id: "u-frank",
          name: "ui-designer",
          display_name: "UI Designer",
          description: "Designer",
          avatar_url: null,
          archived_at: null,
          model: "Claude Code",
          runtime_mode: "local",
          runtime_name: null,
          honor_level: 8,
        },
        {
          id: "a2",
          owner_id: "u-frank",
          name: "beckham",
          display_name: "贝克汉姆",
          description: "Channel Manager",
          avatar_url: null,
          archived_at: null,
          model: "Cursor Agent",
          runtime_mode: "local",
          runtime_name: null,
        },
      ],
      isPending: false,
    });
    runCountsMock.mockReturnValue({ data: [], isPending: false });
  });

  it("renders DESCRIPTION / INFO / full CREATED AGENTS list", () => {
    renderPanel("u-frank");
    expect(screen.getByTestId("member-side-panel")).toBeTruthy();
    expect(screen.getByText("No description")).toBeTruthy();
    expect(screen.getByTestId("member-role-value").textContent).toContain("Owner");
    expect(screen.getByText("me@example.com")).toBeTruthy();
    expect(screen.getAllByTestId("member-created-agent-row")).toHaveLength(2);
    expect(document.querySelector('[data-agent-honor-level="8"]')).toBeInTheDocument();
  });

  it("hides Message for self and shows (you)", () => {
    memberListMock.mockReturnValue({
      data: [
        {
          id: "m-self",
          workspace_id: "ws-1",
          user_id: "u-self",
          role: "member",
          created_at: "2026-06-11T00:00:00Z",
          name: "me",
          display_name: "Me",
          email: "self@example.com",
          avatar_url: null,
          profile_description: "Hello",
        },
      ],
      isPending: false,
    });
    profileMock.mockReturnValue({
      data: {
        member_type: "user",
        member_id: "u-self",
        name: "me",
        display_name: "Me",
        avatar_url: null,
        description: "Hello",
        role: "member",
        status: null,
        recent_activity: [],
        profile_access: "full",
      },
      isPending: false,
    });
    agentsMock.mockReturnValue({ data: [], isPending: false });
    renderPanel("u-self");
    expect(screen.queryByTestId("member-side-panel-message")).toBeNull();
    expect(screen.getByText(/\(you\)/)).toBeTruthy();
    expect(screen.getByText("No agents created yet")).toBeTruthy();
  });

  it("self view (LRM-751): camera badge + name inline edit + 编辑资料 escape hatch", () => {
    memberListMock.mockReturnValue({
      data: [
        {
          id: "m-self",
          workspace_id: "ws-1",
          user_id: "u-self",
          role: "member",
          created_at: "2026-06-11T00:00:00Z",
          name: "me",
          display_name: "Me",
          email: "self@example.com",
          avatar_url: null,
          profile_description: "Hello",
          honor: { level: 42, name_style: "default" },
        },
      ],
      isPending: false,
    });
    profileMock.mockReturnValue({
      data: {
        member_type: "user",
        member_id: "u-self",
        name: "me",
        display_name: "Me",
        avatar_url: null,
        description: "Hello",
        role: "member",
        status: null,
        recent_activity: [],
        profile_access: "full",
      },
      isPending: false,
    });
    agentsMock.mockReturnValue({ data: [], isPending: false });
    const { container } = renderPanel("u-self");
    expect(screen.getByTestId("member-self-avatar-change")).toBeTruthy();
    expect(screen.getByTestId("member-profile-name-trigger").textContent).toContain("Me");
    expect(container.querySelector('[data-user-honor-level="42"]')).not.toBeNull();
    expect(screen.getByTestId("member-profile-description-trigger")).toBeTruthy();
    const editLink = screen.getByTestId("member-side-panel-edit-profile").closest("a");
    expect(editLink?.getAttribute("href")).toBe("/ws-1/settings?tab=profile");
  });

  it("other view (LRM-751): no edit entries at all", () => {
    renderPanel("u-frank");
    expect(screen.queryByTestId("member-self-avatar-change")).toBeNull();
    expect(screen.queryByTestId("member-profile-name-trigger")).toBeNull();
    expect(screen.queryByTestId("member-side-panel-edit-profile")).toBeNull();
    expect(screen.getByTestId("member-side-panel-message")).toBeTruthy();
  });

  // LRM-812: avatar dedup — the top bar leading is name text only (no 22px
  // avatar, no handle); the member's single avatar lives in the 64px body
  // identity block, matching the Agent card chrome. (CREATED AGENTS rows have
  // their own agent avatars — unrelated to the member-identity dedup.)
  it("renders the member avatar exactly once (body identity); top bar is name text only", () => {
    renderPanel("u-frank");

    const memberAvatars = screen.getAllByTestId("avatar-u-frank");
    expect(memberAvatars).toHaveLength(1);

    // The shell header bar (leading + message/close chrome) carries no avatar.
    const closeButton = screen.getByLabelText("Close");
    const headerBar = closeButton.closest("div.border-b");
    expect(headerBar).not.toBeNull();
    expect(headerBar!.querySelector('[data-testid^="avatar-"]')).toBeNull();
    // Name text only — no handle line in the top bar.
    expect(headerBar!.textContent).toContain("Frank An");
    expect(headerBar!.textContent).not.toContain("@frank-an");
  });
});
