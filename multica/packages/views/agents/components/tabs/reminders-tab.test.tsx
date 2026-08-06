// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import type { Agent } from "@multica/core/types";
import type { RawReminderDefinition } from "@multica/core/agents/reminder-view-model";
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
    origin_kind: "agent",
    anchor: {
      available: true,
      kind: "channel",
      display_name: "#deploys",
      href: "/acme/channels/chan-1?message=msg-1",
    },
    ...overrides,
  };
}

function page(definitions: RawReminderDefinition[]) {
  return { definitions, occurrences: [], limit: 20, has_more: false };
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
});

describe("RemindersTab", () => {
  it("renders one flat active-reminder list without Upcoming or History sections", async () => {
    mockGetAgentReminders.mockResolvedValue(
      page([definition({ id: "manual", title: "Manual reminder" })]),
    );

    renderTab();

    expect(await screen.findByText("Manual reminder")).toBeInTheDocument();
    expect(screen.queryByText("Upcoming")).toBeNull();
    expect(screen.queryByText("History")).toBeNull();
    expect(mockGetAgentReminders).toHaveBeenCalledWith(
      "agent-1",
      expect.objectContaining({ status: "scheduled" }),
    );
    expect(mockGetAgentReminders).toHaveBeenCalledTimes(1);
  });

  it("shows one cadence and timezone chip for a recurring calendar reminder", async () => {
    mockGetAgentReminders.mockResolvedValue(
      page([
        definition({
          schedule_kind: "recurring",
          cadence: "daily@09:00",
          schedule_timezone: "Asia/Tokyo",
        }),
      ]),
    );

    renderTab();

    expect(await screen.findByText("daily at 09:00 Asia/Tokyo")).toBeInTheDocument();
  });

  it("shows an interval cadence chip without a timezone", async () => {
    mockGetAgentReminders.mockResolvedValue(
      page([definition({ schedule_kind: "recurring", cadence: "every:30m" })]),
    );

    renderTab();

    expect(await screen.findByText("every 30m")).toBeInTheDocument();
    expect(screen.queryByText(/Asia\/Tokyo/)).toBeNull();
  });

  it("does not show recurrence copy for a one-shot reminder", async () => {
    mockGetAgentReminders.mockResolvedValue(page([definition()]));

    renderTab();

    await screen.findByText("Ping the deploy thread");
    expect(screen.queryByText(/daily at/i)).toBeNull();
    expect(screen.queryByText(/One-time/i)).toBeNull();
  });

  // 08-01 reminder-outage incident: the three status badges were visually
  // identical grey — overdue must stand out at a glance, not just by text.
  it("marks an overdue reminder with the destructive tone and a warning icon", async () => {
    mockGetAgentReminders.mockResolvedValue(
      page([definition({ next_fire_at: "2020-01-01T00:00:00Z" })]),
    );

    renderTab();

    const badge = await screen.findByText("Overdue");
    expect(badge.className).toMatch(/text-destructive/);
    expect(badge.querySelector("svg")).toBeInTheDocument();
  });

  it("keeps a scheduled reminder in the neutral tone with no icon", async () => {
    mockGetAgentReminders.mockResolvedValue(page([definition()]));

    renderTab();

    const badge = await screen.findByText("Scheduled");
    expect(badge.className).not.toMatch(/text-destructive/);
    expect(badge.querySelector("svg")).toBeNull();
  });

  it("shows a safe anchor or an unavailable marker without leaking ids", async () => {
    mockGetAgentReminders.mockResolvedValue(
      page([
        definition({ id: "available", title: "Has anchor" }),
        definition({ id: "unavailable", title: "No anchor", anchor: { available: false } }),
      ]),
    );

    renderTab();

    expect(await screen.findByRole("link", { name: "#deploys" })).toHaveAttribute(
      "href",
      "/acme/channels/chan-1?message=msg-1",
    );
    expect(screen.getByText("Anchor unavailable")).toBeInTheDocument();
    expect(screen.queryByText("unavailable", { exact: true })).toBeNull();
  });

  it("distinguishes fetch errors, forbidden access, and an empty list", async () => {
    mockGetAgentReminders.mockRejectedValueOnce(new Error("network down"));
    const first = renderTab();
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
    first.unmount();

    mockGetAgentReminders.mockRejectedValueOnce(new ApiError("forbidden", 403, "Forbidden"));
    const second = renderTab();
    expect(
      await screen.findByText("You don't have permission to view this agent's reminders."),
    ).toBeInTheDocument();
    second.unmount();

    mockGetAgentReminders.mockResolvedValueOnce(page([]));
    renderTab();
    expect(await screen.findByText("No reminders yet.")).toBeInTheDocument();
  });

  it("exposes zero reminder mutation affordances", async () => {
    mockGetAgentReminders.mockResolvedValue(
      page([definition({ title: "Follow up" })]),
    );

    renderTab();
    await screen.findByText("Follow up");

    for (const button of screen.queryAllByRole("button")) {
      expect(button.textContent).not.toMatch(/schedule|snooze|update|cancel|dismiss|enable/i);
    }
    expect(screen.queryByRole("form")).toBeNull();
    expect(screen.queryByRole("menu")).toBeNull();
  });
});
