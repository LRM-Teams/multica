// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Agent } from "@multica/core/types";
import type {
  RawReminderDefinition,
  RawReminderOccurrence,
  RawReminderPage,
} from "@multica/core/agents/reminder-view-model";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };
const mockGetAgentReminders = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/api")>();
  return {
    ...actual,
    api: { ...actual.api, getAgentReminders: (...args: unknown[]) => mockGetAgentReminders(...args) },
  };
});

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: () => {},
  useWSReconnect: () => {},
}));

import { RemindersTab } from "./reminders-tab";
import { ApiError } from "@multica/core/api";

const agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  workspace_role: "member",
  runtime_id: "runtime-1",
  name: "Agent",
  display_name: "Agent",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  status: "idle",
  max_concurrent_tasks: 1,
  model: "",
  owner_id: "user-1",
  skills: [],
  created_at: "2026-04-16T00:00:00Z",
  updated_at: "2026-04-16T00:00:00Z",
  archived_at: null,
  archived_by: null,
} as Agent;

function definition(overrides: Partial<RawReminderDefinition> = {}): RawReminderDefinition {
  return {
    id: "rem-1",
    title: "Ping the deploy thread",
    status: "scheduled",
    schedule_kind: "one_shot",
    next_fire_at: "2099-07-23T09:00:00Z",
    snooze_count: 0,
    anchor: {
      available: true,
      kind: "channel",
      display_name: "#deploys",
      href: "/acme/channels/chan-1?message=msg-1",
    },
    ...overrides,
  };
}

function occurrence(overrides: Partial<RawReminderOccurrence> = {}): RawReminderOccurrence {
  return {
    id: "occ-1",
    reminder_id: "rem-1",
    title: "Ping the deploy thread",
    status: "fired",
    definition_status: "fired",
    schedule_kind: "one_shot",
    cadence_scheduled_for: "2026-08-11T01:00:00Z",
    due_at: "2026-08-11T01:00:00Z",
    fired_at: "2026-08-11T01:00:01Z",
    anchor: {
      available: true,
      kind: "channel",
      display_name: "#deploys",
      href: "/acme/channels/chan-1?message=msg-1",
    },
    ...overrides,
  };
}

function upcomingPage(definitions: RawReminderDefinition[]): RawReminderPage {
  return { definitions, occurrences: [], limit: 20, has_more: false };
}

function historyPage(
  occurrences: RawReminderOccurrence[],
  pagination: Partial<Pick<RawReminderPage, "has_more" | "next_cursor">> = {},
): RawReminderPage {
  return {
    definitions: [],
    occurrences,
    limit: 20,
    has_more: false,
    ...pagination,
  };
}

function mockReminderPages({
  upcoming = upcomingPage([]),
  history = historyPage([]),
}: {
  upcoming?: RawReminderPage | Error;
  history?: RawReminderPage | Error;
} = {}) {
  mockGetAgentReminders.mockImplementation(
    async (_agentId: string, params: { status: "scheduled" | "fired"; cursor?: string }) => {
      const result = params.status === "scheduled" ? upcoming : history;
      if (result instanceof Error) throw result;
      return result;
    },
  );
}

function renderTab() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={queryClient}>
        <RemindersTab agent={agent} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

beforeEach(() => {
  mockGetAgentReminders.mockReset();
  mockReminderPages();
});

describe("RemindersTab", () => {
  it("renders distinct Upcoming definitions and fired History occurrences", async () => {
    mockReminderPages({
      upcoming: upcomingPage([
        definition({
          id: "recurring",
          title: "Recurring patrol",
          schedule_kind: "recurring",
          cadence: "daily@09:00",
          schedule_timezone: "Asia/Shanghai",
        }),
      ]),
      history: historyPage([
        occurrence({ id: "fired-once", title: "One-shot follow-up" }),
      ]),
    });

    renderTab();

    expect(await screen.findByText("Recurring patrol")).toBeInTheDocument();
    expect(await screen.findByText("One-shot follow-up")).toBeInTheDocument();
    expect(screen.getByText("Upcoming")).toBeInTheDocument();
    expect(screen.getByText("History")).toBeInTheDocument();
    expect(mockGetAgentReminders).toHaveBeenCalledWith(
      "agent-1",
      expect.objectContaining({ status: "scheduled" }),
    );
    expect(mockGetAgentReminders).toHaveBeenCalledWith(
      "agent-1",
      expect.objectContaining({ status: "fired" }),
    );
    expect(mockGetAgentReminders).toHaveBeenCalledTimes(2);
  });

  it("shows one cadence and timezone chip for a recurring calendar reminder", async () => {
    mockReminderPages({
      upcoming: upcomingPage([
        definition({
          schedule_kind: "recurring",
          cadence: "daily@09:00",
          schedule_timezone: "Asia/Tokyo",
        }),
      ]),
    });

    renderTab();

    expect(await screen.findByText("daily at 09:00 Asia/Tokyo")).toBeInTheDocument();
  });

  it("shows an interval cadence chip without a timezone", async () => {
    mockReminderPages({
      upcoming: upcomingPage([
        definition({ schedule_kind: "recurring", cadence: "every:30m" }),
      ]),
    });

    renderTab();

    expect(await screen.findByText("every 30m")).toBeInTheDocument();
    expect(screen.queryByText(/Asia\/Tokyo/)).toBeNull();
  });

  it("shows the one-shot kind without recurring cadence copy", async () => {
    mockReminderPages({ upcoming: upcomingPage([definition()]) });

    renderTab();

    await screen.findByText("Ping the deploy thread");
    expect(screen.queryByText(/daily at/i)).toBeNull();
    expect(screen.getByText("One-time")).toBeInTheDocument();
  });

  // 08-01 reminder-outage incident: the three status badges were visually
  // identical grey — overdue must stand out at a glance, not just by text.
  it("marks an overdue reminder with the destructive tone and a warning icon", async () => {
    mockReminderPages({
      upcoming: upcomingPage([definition({ next_fire_at: "2020-01-01T00:00:00Z" })]),
    });

    renderTab();

    const badge = await screen.findByText("Overdue");
    expect(badge.className).toMatch(/text-destructive/);
    expect(badge.querySelector("svg")).toBeInTheDocument();
  });

  it("keeps a scheduled reminder in the neutral tone with no icon", async () => {
    mockReminderPages({ upcoming: upcomingPage([definition()]) });

    renderTab();

    const badge = await screen.findByText("Scheduled");
    expect(badge.className).not.toMatch(/text-destructive/);
    expect(badge.querySelector("svg")).toBeNull();
  });

  it("shows a safe anchor or an unavailable marker without leaking ids", async () => {
    mockReminderPages({
      upcoming: upcomingPage([
        definition({ id: "available", title: "Has anchor" }),
        definition({ id: "unavailable", title: "No anchor", anchor: { available: false } }),
      ]),
    });

    renderTab();

    expect(await screen.findByRole("link", { name: "#deploys" })).toHaveAttribute(
      "href",
      "/acme/channels/chan-1?message=msg-1",
    );
    expect(screen.getByText("Anchor unavailable")).toBeInTheDocument();
    expect(screen.queryByText("unavailable", { exact: true })).toBeNull();
  });

  it("distinguishes fetch errors, forbidden access, and independent empty sections", async () => {
    mockReminderPages({ upcoming: new Error("network down") });
    const first = renderTab();
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    first.unmount();

    mockReminderPages({
      history: new ApiError("forbidden", 403, "Forbidden"),
    });
    const second = renderTab();
    expect(
      await screen.findByText("You don't have permission to view this agent's reminders."),
    ).toBeInTheDocument();
    second.unmount();

    mockReminderPages();
    renderTab();
    expect(await screen.findByText("No upcoming reminders.")).toBeInTheDocument();
    expect(screen.getByText("No fired reminders yet.")).toBeInTheDocument();
  });

  it("paginates History without changing Reminder state", async () => {
    mockGetAgentReminders.mockImplementation(
      async (_agentId: string, params: { status: "scheduled" | "fired"; cursor?: string }) => {
        if (params.status === "scheduled") return upcomingPage([]);
        if (params.cursor === "older") {
          return historyPage([occurrence({ id: "older", title: "Older fire" })]);
        }
        return historyPage(
          [occurrence({ id: "newer", title: "Newer fire", definition_status: "scheduled" })],
          { has_more: true, next_cursor: "older" },
        );
      },
    );

    renderTab();

    expect(await screen.findByText("Newer fire")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByText("Older fire")).toBeInTheDocument();
    await waitFor(() =>
      expect(mockGetAgentReminders).toHaveBeenCalledWith(
        "agent-1",
        expect.objectContaining({ status: "fired", cursor: "older" }),
      ),
    );
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("exposes zero reminder mutation affordances", async () => {
    mockReminderPages({
      upcoming: upcomingPage([definition({ title: "Follow up" })]),
      history: historyPage([occurrence({ title: "Already fired" })]),
    });

    renderTab();
    await screen.findByText("Follow up");

    for (const button of screen.queryAllByRole("button")) {
      expect(button.textContent).not.toMatch(/schedule|snooze|update|cancel|dismiss|enable/i);
    }
    expect(screen.queryByRole("form")).toBeNull();
    expect(screen.queryByRole("menu")).toBeNull();
  });
});
