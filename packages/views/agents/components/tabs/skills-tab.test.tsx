// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import type { Agent, GeneratedSkillDelivery } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enAgents from "../../../locales/en/agents.json";

const TEST_RESOURCES = { en: { common: enCommon, agents: enAgents } };

const mockListSkills = vi.hoisted(() => vi.fn());
const mockListGeneratedSkillDeliveries = vi.hoisted(() => vi.fn());
const mockDecideGeneratedSkillDelivery = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listSkills: (...args: unknown[]) => mockListSkills(...args),
    setAgentSkills: vi.fn(),
    listAgentGeneratedSkillDeliveries: (...args: unknown[]) =>
      mockListGeneratedSkillDeliveries(...args),
    decideAgentGeneratedSkillDelivery: (...args: unknown[]) =>
      mockDecideGeneratedSkillDelivery(...args),
  },
}));

vi.mock("sonner", () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

import { SkillsTab, getGeneratedSkillAwaitingHintKey } from "./skills-tab";

const agent: Agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  name: "Agent",
  display_name: "Agent",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
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

function makeGeneratedDelivery(
  overrides: Partial<GeneratedSkillDelivery> = {},
): GeneratedSkillDelivery {
  return {
    id: "delivery-1",
    workspace_id: "ws-1",
    unit_id: "unit-1",
    version_id: "version-1",
    target_agent_id: "agent-1",
    delivery_type: "generated",
    status: "delivered",
    reason: "source agent match",
    matcher_score: 1,
    unit_type: "skill",
    title: "Review Helper",
    canonical_summary: "Check pull requests",
    content: "",
    metadata: {},
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
    mockListGeneratedSkillDeliveries.mockResolvedValue({ deliveries: [] });
    mockDecideGeneratedSkillDelivery.mockResolvedValue(undefined);
  });

  it("does not render the inline Local Runtime Skills section even for local-runtime agents", async () => {
    // The inline section auto-loaded local skills on every Skills-tab
    // entry, which was both noisy and (under multi-replica deploys) prone
    // to "request not found" because the request store is in-process.
    // Local-skill import now lives behind the explicit Skills page →
    // Add Skill → From Runtime tab; nothing here may auto-load.
    renderSkillsTab();

    // Top informational callout should still render; that's how we know
    // the tab body itself rendered (not stuck in a loading state).
    expect(
      await screen.findByText(/Local runtime skills are always available/i),
    ).toBeInTheDocument();

    // The removed section's heading and its trigger button must be gone.
    expect(screen.queryByText("Local Runtime Skills")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Import to Workspace/i }),
    ).not.toBeInTheDocument();

    // No runtime list / local-skills query should be wired up either —
    // we removed @multica/core/runtimes from this file's imports.
    // Surface it via behaviour: the `agent` here has runtime_id but the
    // tab must not invoke any runtime-list mock to render. (Both are
    // already deleted from the mock setup above; this assertion is
    // implicit — the test file would fail to import if the component
    // still referenced runtimeListOptions / runtimeLocalSkillsOptions.)
  });

  it("shows accept actions only while a generated skill awaits a decision", async () => {
    mockListGeneratedSkillDeliveries.mockResolvedValue({
      deliveries: [makeGeneratedDelivery({ status: "delivered" })],
    });

    renderSkillsTab();

    expect(await screen.findByRole("button", { name: /Accept/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Ignore/i })).toBeInTheDocument();
  });

  it("hides accept actions after a generated skill is accepted but not yet enabled locally", async () => {
    mockListGeneratedSkillDeliveries.mockResolvedValue({
      deliveries: [
        makeGeneratedDelivery({
          status: "accepted",
          delivered_path: "/agents/agent-1/skills/generated/unit-1",
        }),
      ],
    });

    renderSkillsTab();

    expect(await screen.findByText("Accepted")).toBeInTheDocument();
    expect(
      screen.getByText(/Waiting for the local runtime to enable this skill/i),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Accept/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Ignore/i })).not.toBeInTheDocument();
  });

  it("shows a local-runtime hint when accepted before the daemon has written anything", async () => {
    mockListGeneratedSkillDeliveries.mockResolvedValue({
      deliveries: [makeGeneratedDelivery({ status: "accepted", delivered_path: "" })],
    });

    renderSkillsTab();

    expect(
      await screen.findByText(/Waiting for the local runtime to receive this skill/i),
    ).toBeInTheDocument();
  });

  it("shows enabled status without actions after the runtime enables the generated skill", async () => {
    mockListGeneratedSkillDeliveries.mockResolvedValue({
      deliveries: [
        makeGeneratedDelivery({
          status: "accepted",
          delivered_path: "/agents/agent-1/skills/enabled/unit-1",
        }),
      ],
    });

    renderSkillsTab();

    expect(await screen.findByText("Enabled")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Accept/i })).not.toBeInTheDocument();
  });

  it("maps awaiting hint keys from delivery path state", () => {
    expect(
      getGeneratedSkillAwaitingHintKey(makeGeneratedDelivery({ status: "accepted", delivered_path: "" })),
    ).toBe("generated_accepted_waiting_local_hint");
    expect(
      getGeneratedSkillAwaitingHintKey(
        makeGeneratedDelivery({
          status: "accepted",
          delivered_path: "/agents/agent-1/skills/generated/unit-1",
        }),
      ),
    ).toBe("generated_accepted_pending_hint");
    expect(
      getGeneratedSkillAwaitingHintKey(
        makeGeneratedDelivery({
          status: "accepted",
          delivered_path: "/agents/agent-1/skills/enabled/unit-1",
        }),
      ),
    ).toBeNull();
  });
});
