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
  deriveRuntimeHealth: (runtime: { status: string }) =>
    runtime.status === "online" ? "online" : "offline",
}));

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: { ensureWindy: vi.fn() },
}));

vi.mock("../agents/components/model-dropdown", () => ({
  ModelDropdown: () => <div data-testid="model-dropdown" />,
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
          setup_title: "Meet Wendy",
          setup_description: "Choose runtime and model",
          setup_runtime: "Runtime",
          setup_runtime_placeholder: "Select runtime",
          setup_model: "Model",
          setup_connect_title: "Connect a Computer",
          setup_connect_description:
            "Install and connect a Computer before creating Wendy.",
          setup_listening: "Listening for your Computer…",
          setup_creating: "Creating…",
          setup_create: "Create Wendy",
          setup_failed: "failed",
          setup_runtime_offline: "offline hint",
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
  it("embeds computer install instructions when no online Runtime exists", () => {
    runtimeState.runtimes = [];
    render(<OnboardingAgentSetup workspace={workspace} />);

    expect(screen.getByTestId("onboarding-agent-connect-computer")).toBeInTheDocument();
    expect(screen.getByTestId("cli-install-instructions")).toHaveAttribute(
      "data-slug",
      "acme",
    );
    expect(screen.getByText("Listening for your Computer…")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create Wendy" })).toBeDisabled();
    expect(screen.queryByTestId("model-dropdown")).toBeNull();
  });

  it("shows Create Wendy controls once an online Runtime is available", () => {
    runtimeState.runtimes = [
      { id: "rt-1", name: "My Mac", status: "online", last_seen_at: new Date().toISOString() },
    ];
    render(<OnboardingAgentSetup workspace={workspace} />);

    expect(screen.queryByTestId("onboarding-agent-connect-computer")).toBeNull();
    expect(screen.getByTestId("model-dropdown")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: /runtime/i })).toBeInTheDocument();
  });
});
