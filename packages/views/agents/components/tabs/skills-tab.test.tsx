// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import type { Agent, AgentSkillSuggestion } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

const mockListSkills = vi.hoisted(() => vi.fn());
const mockListSkillSuggestions = vi.hoisted(() => vi.fn());
const mockDecideSkillSuggestion = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listSkills: (...args: unknown[]) => mockListSkills(...args),
    setAgentSkills: vi.fn(),
    listAgentSkillSuggestions: (...args: unknown[]) =>
      mockListSkillSuggestions(...args),
    decideAgentSkillSuggestion: (...args: unknown[]) =>
      mockDecideSkillSuggestion(...args),
  },
}));

vi.mock("sonner", () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

import { SkillsTab } from "./skills-tab";

const agent: Agent = {
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
};

function makeSuggestion(
  overrides: Partial<AgentSkillSuggestion> = {},
): AgentSkillSuggestion {
  return {
    id: "suggestion-1",
    workspace_id: "ws-1",
    agent_id: "agent-1",
    skill_id: "skill-1",
    action: "add",
    reason: "metadata match",
    matcher_score: 0.8,
    status: "pending",
    skill_name: "Review Helper",
    skill_description: "Check pull requests",
    created_at: "2026-04-16T00:00:00Z",
    updated_at: "2026-04-16T00:00:00Z",
    ...overrides,
  };
}

function renderSkillsTab() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });

  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={queryClient}>
        <SkillsTab agent={agent} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("SkillsTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockListSkills.mockResolvedValue([]);
    mockListSkillSuggestions.mockResolvedValue({ suggestions: [] });
    mockDecideSkillSuggestion.mockResolvedValue(undefined);
  });

  it("does not render the inline Local Runtime Skills section even for local-runtime agents", async () => {
    renderSkillsTab();

    expect(
      await screen.findByText(/Local runtime skills are always available/i),
    ).toBeInTheDocument();

    expect(screen.queryByText("Local Runtime Skills")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Import to Workspace/i }),
    ).not.toBeInTheDocument();
  });

  it("shows accept and dismiss actions for pending skill suggestions", async () => {
    mockListSkillSuggestions.mockResolvedValue({
      suggestions: [makeSuggestion()],
    });

    renderSkillsTab();

    expect(await screen.findByRole("button", { name: /Accept/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Dismiss/i })).toBeInTheDocument();
    expect(screen.getByText("Suggested add")).toBeInTheDocument();
  });

  it("shows remove suggestion badge for remove actions", async () => {
    mockListSkillSuggestions.mockResolvedValue({
      suggestions: [makeSuggestion({ action: "remove" })],
    });

    renderSkillsTab();

    expect(await screen.findByText("Suggested remove")).toBeInTheDocument();
  });

  it("shows empty state when there are no pending suggestions", async () => {
    renderSkillsTab();

    expect(await screen.findByText("No pending skill suggestions.")).toBeInTheDocument();
  });
});
