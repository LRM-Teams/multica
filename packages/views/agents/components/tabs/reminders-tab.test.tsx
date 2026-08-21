// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Agent } from "@multica/core/types";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";

const getAgentReminders = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/api")>();
  return {
    ...actual,
    api: { ...actual.api, getAgentReminders },
  };
});
vi.mock("@multica/core/realtime", () => ({
  useWSEvent: () => {},
  useWSReconnect: () => {},
}));

import { RemindersTab } from "./reminders-tab";

const agent = {
  id: "agent-1",
  workspace_id: "workspace-1",
  workspace_role: "member",
  runtime_id: "runtime-1",
  name: "atlas",
  display_name: "Atlas",
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
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  archived_at: null,
  archived_by: null,
} as Agent;

function renderTab() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, agents: enAgents } }}>
      <QueryClientProvider client={queryClient}>
        <RemindersTab agent={agent} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("RemindersTab", () => {
  beforeEach(() => {
    getAgentReminders.mockReset();
    getAgentReminders.mockResolvedValue({ definitions: [] });
  });

  it("renders the reminder list without section headings", async () => {
    renderTab();

    expect(await screen.findByText("No reminders.")).toBeInTheDocument();
    expect(screen.queryByText("Upcoming")).not.toBeInTheDocument();
    expect(screen.queryByText("History")).not.toBeInTheDocument();
    expect(screen.queryByText("No fired reminders yet.")).not.toBeInTheDocument();
    await waitFor(() => expect(getAgentReminders).toHaveBeenCalledWith("agent-1"));
    expect(getAgentReminders).toHaveBeenCalledTimes(1);
  });

  it("still renders upcoming reminder definitions", async () => {
    getAgentReminders.mockResolvedValue({
      definitions: [
        {
          id: "reminder-1",
          title: "Ping standup",
          status: "scheduled",
          scheduleKind: "one_shot",
          nextFireAt: "2099-07-24T09:00:00Z",
          snoozeCount: 0,
          anchor: { available: false },
        },
      ],
    });

    renderTab();

    expect(await screen.findByText("Ping standup")).toBeInTheDocument();
    expect(screen.getByText("Scheduled")).toBeInTheDocument();
  });
});
