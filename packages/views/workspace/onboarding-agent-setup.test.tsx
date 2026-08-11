// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { OnboardingAgentSetup } from "./onboarding-agent-setup";

const runtimeState = vi.hoisted(() => ({
  runtimes: [] as Array<{
    id: string;
    name: string;
    status: string;
    last_seen_at?: string | null;
    daemon_id?: string;
    provider?: string;
    runtime_mode?: string;
    computer_connected?: boolean;
  }>,
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: unknown[] }) => {
    const key = options.queryKey ?? [];
    if (key.includes("members") || JSON.stringify(key).includes("members")) {
      return { data: [{ user_id: "owner-1", role: "owner" }], isLoading: false };
    }
    return { data: runtimeState.runtimes, isLoading: false };
  },
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (select: (state: { user: { id: string } }) => unknown) =>
    select({ user: { id: "owner-1" } }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members"] }),
  workspaceKeys: {
    list: () => ["workspaces"],
    agents: (id: string) => ["agents", id],
  },
}));

vi.mock("@multica/core/runtimes", () => ({
  runtimeListOptions: (wsId: string) => ({ queryKey: ["runtimes", wsId] }),
  runtimeKeys: { all: (wsId: string) => ["runtimes", wsId] },
  runtimeModelsOptions: () => ({ queryKey: ["models"] }),
  runtimeCurrentVersion: () => "1.0.0",
  aggregateRuntimeHealthPresentation: () => "ok",
}));

vi.mock("../agents/components/use-execution-selection", () => ({
  useExecutionSelection: () => ({
    machineId: "machine-1",
    machineRuntimes: runtimeState.runtimes,
    runtimeId: runtimeState.runtimes[0]?.id ?? "",
    model: "",
    thinkingLevel: "",
    selectMachine: vi.fn(),
    selectRuntime: vi.fn(),
    selectModel: vi.fn(),
    selectThinking: vi.fn(),
  }),
}));

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: { ensureWindy: vi.fn() },
}));

vi.mock("../agents/components/execution-config-fields", () => ({
  ExecutionConfigFields: () => <div data-testid="execution-config-fields" />,
}));

vi.mock("../onboarding/steps/cli-install-instructions", () => ({
  CliInstallInstructions: ({ workspaceSlug }: { workspaceSlug?: string }) => (
    <div data-testid="cli-install-instructions" data-slug={workspaceSlug ?? ""}>
      install-computer
    </div>
  ),
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (selector: (catalog: { windy: Record<string, string> }) => unknown) =>
      selector({
        windy: {
          setup_waiting_title: "waiting",
          setup_waiting_description: "wait desc",
          setup_steps_label: "Workspace setup steps",
          setup_step_computer: "Computer",
          setup_step_wendy: "Wendy",
          setup_step1_title: "Connect a Computer",
          setup_step1_description:
            "Install and connect a Computer before creating Wendy.",
          setup_step2_title: "Meet Wendy",
          setup_step2_description: "Choose computer, runtime, model, reasoning",
          setup_listening: "Listening for your Computer…",
          setup_creating: "Creating…",
          setup_create: "Create Wendy",
          setup_failed: "failed",
        },
      }),
  }),
}));

const workspace = {
  id: "ws-1",
  name: "Acme",
  slug: "acme",
  onboarding_agent_id: null,
} as unknown as Parameters<typeof OnboardingAgentSetup>[0]["workspace"];

describe("OnboardingAgentSetup", () => {
  it("step 1: requires Computer setup and does not show Create Wendy", () => {
    runtimeState.runtimes = [];
    render(<OnboardingAgentSetup workspace={workspace} />);

    expect(screen.getByTestId("onboarding-agent-setup-steps")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Connect a Computer" })).toBeInTheDocument();
    expect(screen.getByTestId("onboarding-agent-connect-computer")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Create Wendy" })).toBeNull();
    expect(screen.queryByTestId("execution-config-fields")).toBeNull();
  });

  it("step 2 follows server-owned Computer connectivity, not runtime timestamps", () => {
    runtimeState.runtimes = [
      {
        id: "rt-1",
        name: "My Mac Claude",
        status: "offline",
        last_seen_at: "2026-01-01T00:00:00Z",
        daemon_id: "daemon-1",
        provider: "claude",
        runtime_mode: "local",
        computer_connected: true,
      },
    ];
    render(<OnboardingAgentSetup workspace={workspace} />);

    expect(screen.getByRole("heading", { name: "Meet Wendy" })).toBeInTheDocument();
    expect(screen.getByTestId("onboarding-agent-create-wendy")).toBeInTheDocument();
    expect(screen.getByTestId("execution-config-fields")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create Wendy" })).toBeInTheDocument();
    expect(screen.queryByTestId("onboarding-agent-connect-computer")).toBeNull();
  });

  it("does not treat a fresh runtime timestamp as a connected Computer", () => {
    runtimeState.runtimes = [
      {
        id: "rt-1",
        name: "My Mac Claude",
        status: "online",
        last_seen_at: new Date().toISOString(),
        daemon_id: "daemon-1",
        provider: "claude",
        runtime_mode: "local",
        computer_connected: false,
      },
    ];
    render(<OnboardingAgentSetup workspace={workspace} />);

    expect(screen.getByRole("heading", { name: "Connect a Computer" })).toBeInTheDocument();
    expect(screen.queryByTestId("execution-config-fields")).toBeNull();
  });
});
