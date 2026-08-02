// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ChannelGoalSubgoal } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import { countOpenSubgoals, GoalSubgoalPanel } from "./goal-subgoal-panel";

const state = vi.hoisted(() => ({
  subgoals: [] as ChannelGoalSubgoal[],
}));

vi.mock("@multica/core/channels", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/channels")>()),
  channelGoalSubgoalsOptions: (channelId: string) => ({
    queryKey: ["channel-goal", channelId, "subgoals"],
    queryFn: async () => ({ subgoals: state.subgoals }),
  }),
  channelMembersOptions: (channelId: string) => ({
    queryKey: ["channel-members", channelId],
    queryFn: async () => [
      {
        member_type: "agent" as const,
        member_id: "agent-1",
        name: "fe",
        display_name: "Frontend",
      },
    ],
  }),
  useCreateChannelGoalSubgoal: () => ({ mutate: vi.fn(), isPending: false }),
  useUpdateChannelGoalSubgoal: () => ({ mutate: vi.fn(), isPending: false }),
  useResolveChannelGoalSubgoal: () => ({ mutate: vi.fn(), isPending: false }),
  useClearChannelGoalSubgoalWaitingOn: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

function subgoal(overrides: Partial<ChannelGoalSubgoal> = {}): ChannelGoalSubgoal {
  return {
    id: "sg-1",
    workspace_id: "ws-1",
    channel_id: "channel-1",
    goal_id: "goal-1",
    title: "Ship FE panel",
    purpose: "Orchestrate open subgoals",
    completion_boundary: "PR merged",
    brief: "Match LRM-1003",
    current_conclusion: "",
    status: "captured",
    version: 1,
    responsible_type: "agent",
    responsible_id: "agent-1",
    participants: [],
    depends_on: [],
    waiting_on: null,
    artifact_refs: ["https://example.com/design"],
    activity_delta: [],
    created_by_type: "user",
    created_by_id: "user-1",
    updated_by_type: "user",
    updated_by_id: "user-1",
    created_at: "2026-08-02T00:00:00Z",
    updated_at: "2026-08-02T00:00:00Z",
    ...overrides,
  };
}

function renderPanel() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, channels: enChannels } }}>
      <QueryClientProvider client={queryClient}>
        <GoalSubgoalPanel channelId="channel-1" canManage onClose={() => undefined} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("countOpenSubgoals", () => {
  it("counts captured/in_progress/waiting only", () => {
    expect(
      countOpenSubgoals([
        subgoal({ status: "captured" }),
        subgoal({ id: "2", status: "in_progress" }),
        subgoal({ id: "3", status: "waiting" }),
        subgoal({ id: "4", status: "resolved" }),
        subgoal({ id: "5", status: "cancelled" }),
      ]),
    ).toBe(3);
  });
});

describe("GoalSubgoalPanel", () => {
  it("shows empty state when nothing is captured", async () => {
    state.subgoals = [];
    renderPanel();
    expect(await screen.findByTestId("subgoals-empty")).toBeInTheDocument();
    expect(screen.getByText(/No subgoals captured yet/i)).toBeInTheDocument();
  });

  it("lists open subgoals with responsible and waiting_on", async () => {
    state.subgoals = [
      subgoal({
        waiting_on: { kind: "external", note: "design freeze" },
        status: "waiting",
      }),
    ];
    renderPanel();
    expect(await screen.findByText("Ship FE panel")).toBeInTheDocument();
    expect(screen.getByText(/Responsible · Frontend/i)).toBeInTheDocument();
    expect(screen.getByText(/waiting_on · design freeze/i)).toBeInTheDocument();
  });

  it("opens detail and shows non-cascade resolve copy", async () => {
    const user = userEvent.setup();
    state.subgoals = [subgoal({ status: "in_progress" })];
    renderPanel();
    await user.click(await screen.findByTestId("subgoal-row-sg-1"));
    expect(await screen.findByTestId("subgoal-detail")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Complete subgoal/i }));
    expect(
      screen.getByText(/will not close automatically/i),
    ).toBeInTheDocument();
  });
});
