import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelGoal } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enChannels from "../../locales/en/channels.json";
import enKnowledge from "../../locales/en/knowledge.json";
import { ChannelGoalCard } from "./channel-goal-card";

const state = vi.hoisted(() => ({
  goal: null as ChannelGoal | null,
  create: vi.fn(),
  update: vi.fn(),
}));

vi.mock("@multica/core/channels", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/channels")>()),
  channelGoalOptions: (channelId: string) => ({
    queryKey: ["channel-goal", channelId],
    queryFn: async () => ({ goal: state.goal }),
  }),
  channelMembersOptions: (channelId: string) => ({
    queryKey: ["channel-members", channelId],
    queryFn: async () => [],
  }),
  channelGoalProcessesOptions: (channelId: string) => ({
    queryKey: ["channel-goal", channelId, "process"],
    queryFn: async () => ({ goal_id: "", processes: [] }),
  }),
  channelGoalProcessOptions: (channelId: string, managerId: string) => ({
    queryKey: ["channel-goal", channelId, "process", managerId],
    queryFn: async () => ({ process: null }),
    enabled: !!managerId,
  }),
  channelGoalSubgoalsOptions: (channelId: string) => ({
    queryKey: ["channel-goal", channelId, "subgoals"],
    queryFn: async () => ({ subgoals: [] }),
  }),
  useCreateChannelGoal: () => ({
    mutate: state.create,
    isPending: false,
  }),
  useUpdateChannelGoal: () => ({
    mutate: state.update,
    isPending: false,
  }),
}));

vi.mock("@multica/core/paths", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/paths")>();
  return {
    ...actual,
    useWorkspacePaths: () => actual.paths.workspace("acme"),
    useRequiredWorkspaceSlug: () => "acme",
  };
});

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ name }: { name?: string }) => <span data-testid="actor-avatar">{name}</span>,
}));

vi.mock("../../navigation", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../navigation")>();
  return {
    ...actual,
    AppLink: ({
      href,
      children,
      className,
    }: {
      href: string;
      children?: React.ReactNode;
      className?: string;
    }) => (
      <a href={href} className={className}>
        {children}
      </a>
    ),
    useNavigation: () => ({ push: vi.fn(), replace: vi.fn() }),
  };
});

function goal(overrides: Partial<ChannelGoal> = {}): ChannelGoal {
  return {
    id: "goal-1",
    workspace_id: "ws-1",
    channel_id: "channel-1",
    title: "Ship adaptive goals",
    objective: "Keep long-running work aligned",
    success_criteria: ["Goal is visible", "Goal survives resume"],
    status: "active",
    version: 3,
    progress_summary: "Backend is ready",
    current_step: "Finish the UI",
    blocker: "",
    evidence_refs: ["test:backend"],
    completed_criteria: [],
    created_by_type: "user",
    created_by_id: "user-1",
    updated_by_type: "user",
    updated_by_id: "user-1",
    created_at: "2026-07-31T00:00:00Z",
    updated_at: "2026-07-31T00:00:00Z",
    ...overrides,
  };
}

function renderCard(canManage = true) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  queryClient.setQueryData(["channel-goal", "channel-1"], { goal: state.goal });
  return render(
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, channels: enChannels, knowledge: enKnowledge } }}
    >
      <QueryClientProvider client={queryClient}>
        <ChannelGoalCard channelId="channel-1" canManage={canManage} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ChannelGoalCard", () => {
  beforeEach(() => {
    state.goal = null;
    state.create.mockReset();
    state.update.mockReset();
  });

  it("keeps ordinary channels clean for viewers without goal authority", () => {
    renderCard(false);
    expect(screen.queryByText("Set manually")).not.toBeInTheDocument();
    expect(screen.queryByTestId("channel-goal-card")).not.toBeInTheDocument();
  });

  it("creates a goal from the demoted manual text link", async () => {
    const user = userEvent.setup();
    state.create.mockImplementation((_input, options) => options?.onSuccess?.());
    renderCard();
    expect(
      screen.getByText(/State the overall goal in the group/i),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Set manually" }));
    await user.type(screen.getByLabelText("Goal title"), "Launch v1");
    await user.type(screen.getByLabelText("Outcome"), "Ship a durable goal mode");
    await user.type(screen.getByLabelText("Success criteria"), "Visible in channel\nSurvives resume");
    await user.click(screen.getByRole("button", { name: "Start goal" }));
    expect(state.create).toHaveBeenCalledWith(
      {
        title: "Launch v1",
        objective: "Ship a durable goal mode",
        success_criteria: ["Visible in channel", "Survives resume"],
      },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    );
  });

  it("opens the inline process viewer from the top Goal entry", async () => {
    const user = userEvent.setup();
    state.goal = goal();
    renderCard();
    await user.click(screen.getByTestId("channel-goal-process-entry"));
    expect(screen.getByTestId("goal-process-panel")).toBeInTheDocument();
  });

  it("updates progress, evidence, and criteria as one versioned write", async () => {
    const user = userEvent.setup();
    state.goal = goal();
    state.update.mockImplementation((_input, options) => options?.onSuccess?.());
    renderCard();
    await user.click(screen.getByRole("button", { name: "Expand goal" }));
    expect(screen.getByText("test:backend")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Complete goal" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Update progress" }));
    const criteria = screen.getAllByRole("checkbox");
    await user.click(criteria[0]!);
    await user.click(criteria[1]!);
    await user.clear(screen.getByLabelText("Current step"));
    await user.type(screen.getByLabelText("Current step"), "Final review");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(state.update).toHaveBeenCalledWith(
      expect.objectContaining({
        expected_version: 3,
        current_step: "Final review",
        completed_criteria: ["Goal is visible", "Goal survives resume"],
        evidence_refs: ["test:backend"],
      }),
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    );
  });

});
