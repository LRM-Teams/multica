// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";
import { DeleteComputerDialog } from "./delete-computer-dialog";
import { parseComputerDeleteConflict } from "./delete-computer-conflict";
import type { RuntimeMachine } from "./runtime-machines";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

const { ApiError, apiDeleteByDaemon } = vi.hoisted(() => {
  class ApiError extends Error {
    status: number;
    statusText: string;
    body: unknown;
    constructor(
      message: string,
      status: number,
      statusText: string,
      body: unknown,
    ) {
      super(message);
      this.status = status;
      this.statusText = statusText;
      this.body = body;
    }
  }
  return {
    ApiError,
    apiDeleteByDaemon: vi.fn(),
  };
});

vi.mock("@multica/core/api", () => ({
  api: {
    deleteRuntimesByDaemon: (...args: unknown[]) => apiDeleteByDaemon(...args),
    listMembers: vi.fn(async () => []),
  },
  ApiError,
}));

vi.mock("@multica/core/runtimes/mutations", () => ({
  useDeleteRuntimesByDaemon: () => ({
    isPending: false,
    mutateAsync: (...args: unknown[]) => apiDeleteByDaemon(...args),
  }),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/agents/${id}`,
  }),
}));

vi.mock("../../navigation/app-link", () => ({
  AppLink: ({
    href,
    children,
    className,
  }: {
    href: string;
    children: React.ReactNode;
    className?: string;
  }) => (
    <a href={href} className={className}>
      {children}
    </a>
  ),
}));

vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("../../common/actor-identity-row", () => ({
  ActorIdentityRow: ({ displayName }: { displayName: string }) => (
    <span>{displayName}</span>
  ),
}));

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    name: "Claude (build-01)",
    provider: "claude",
    status: "offline",
    device_info: "build-01",
    daemon_id: "daemon-1",
    runtime_mode: "local",
    owner_id: "user-me",
    visibility: "private",
    last_seen_at: new Date().toISOString(),
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  } as AgentRuntime;
}

function makeMachine(
  overrides: Partial<RuntimeMachine> = {},
): RuntimeMachine {
  const runtime = makeRuntime();
  return {
    id: "local:daemon-1",
    daemonId: "daemon-1",
    title: "build-01",
    subtitle: null,
    deviceInfo: "build-01",
    cliVersion: "1.0.0",
    mode: "local",
    section: "remote",
    isCurrent: false,
    health: "offline",
    runtimeHealth: null,
    updateError: null,
    updateTargetVersion: null,
    runtimes: [runtime],
    onlineCount: 0,
    issueCount: 1,
    runningCount: 0,
    queuedCount: 0,
    providerNames: ["claude"],
    lastSeenAt: runtime.last_seen_at,
    ...overrides,
  };
}

function renderDialog(
  props: Partial<React.ComponentProps<typeof DeleteComputerDialog>> = {},
) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const machine = props.machine ?? makeMachine();
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <DeleteComputerDialog
          open
          onOpenChange={vi.fn()}
          machine={machine}
          daemonId={machine.daemonId ?? "daemon-1"}
          wsId="ws-1"
          onDeleted={vi.fn()}
          {...props}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("parseComputerDeleteConflict", () => {
  it("maps structured 409 codes", () => {
    const err = new ApiError("conflict", 409, "Conflict", {
      code: "computer_has_online_runtimes",
      error: "still online",
    });
    expect(parseComputerDeleteConflict(err)?.code).toBe(
      "computer_has_online_runtimes",
    );
  });

  it("extracts active agents for computer_has_active_agents", () => {
    const err = new ApiError("conflict", 409, "Conflict", {
      code: "computer_has_active_agents",
      active_agents: [{ id: "a1", name: "Agent One" }],
    });
    const parsed = parseComputerDeleteConflict(err);
    expect(parsed?.activeAgents).toHaveLength(1);
    expect(parsed?.activeAgents[0]?.id).toBe("a1");
  });

  it("returns null for non-409", () => {
    expect(
      parseComputerDeleteConflict(new ApiError("nope", 500, "Error", {})),
    ).toBeNull();
  });
});

describe("DeleteComputerDialog", () => {
  beforeEach(() => {
    apiDeleteByDaemon.mockReset();
  });

  it("calls deleteRuntimesByDaemon with daemon id + runtime mode", async () => {
    apiDeleteByDaemon.mockResolvedValue({
      status: "ok",
      daemon_id: "daemon-1",
      deleted_count: 2,
      deleted_runtime_ids: ["rt-1", "rt-2"],
    });
    const onDeleted = vi.fn();
    renderDialog({ onDeleted });

    fireEvent.click(
      screen.getByRole("button", { name: /delete computer/i }),
    );

    await waitFor(() => {
      expect(apiDeleteByDaemon).toHaveBeenCalledWith({
        daemonId: "daemon-1",
        runtimeMode: "local",
      });
    });
    expect(onDeleted).toHaveBeenCalled();
  });

  it("surfaces computer_has_active_tasks without falling back to row delete", async () => {
    apiDeleteByDaemon.mockRejectedValue(
      new ApiError("blocked", 409, "Conflict", {
        code: "computer_has_active_tasks",
        error: "active tasks",
      }),
    );
    renderDialog();

    fireEvent.click(
      screen.getByRole("button", { name: /delete computer/i }),
    );

    await waitFor(() => {
      expect(
        screen.getByText(/active tasks still running/i),
      ).toBeTruthy();
    });
    expect(apiDeleteByDaemon).toHaveBeenCalledTimes(1);
  });
});
