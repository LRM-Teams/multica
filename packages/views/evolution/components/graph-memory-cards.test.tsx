// @vitest-environment jsdom

import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import { EMPTY_GRAPH_MEMORY_PROFILE } from "@multica/core/api/schemas";
import { evolutionKeys } from "@multica/core/evolution/queries";
import { runtimeModelsKeys } from "@multica/core/runtimes";
import { api, ApiError } from "@multica/core/api";
import type { RuntimeDevice } from "@multica/core/types";
import enAgents from "../../locales/en/agents.json";
import enEvolution from "../../locales/en/evolution.json";
import {
  GraphMemoryAgentModeCard,
  GraphMemoryStatusCard,
  LegacyCurationNotApplicableCard,
  MemoryHealthCard,
  RetentionCard,
  TrainingGovernanceCard,
} from "./graph-memory-cards";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn(), custom: vi.fn(), dismiss: vi.fn(), message: vi.fn(), info: vi.fn(), warning: vi.fn(), promise: vi.fn() },
}));

vi.mock("@multica/ui/lib/error-toast", () => ({
  showErrorToast: vi.fn(),
}));

vi.mock("@multica/ui/components/ui/switch", () => ({
  // fireEvent.click does not toggle controlled checkboxes the way user-event
  // does, so derive the next state from the controlled prop instead.
  Switch: ({ checked, onCheckedChange, ...props }: { checked: boolean; onCheckedChange?: (checked: boolean) => void } & Record<string, unknown>) => (
    <input
      type="checkbox"
      role="switch"
      checked={checked}
      onChange={() => onCheckedChange?.(!checked)}
      {...props}
    />
  ),
}));

vi.mock("@multica/core/api", () => {
  class ApiErrorImpl extends Error {
    status: number;
    constructor(message: string, status: number, _statusText?: string, _body?: unknown) {
      super(message);
      this.status = status;
    }
  }
  return {
    api: {
      getTrainingGovernance: vi.fn(),
      updateTrainingGrant: vi.fn(),
      updateTrainingPolicy: vi.fn(),
      getMemoryRetention: vi.fn(),
      updateMemoryRetention: vi.fn(),
      getGraphMemoryStatus: vi.fn(),
      getGraphMemoryAudit: vi.fn(),
    },
    ApiError: ApiErrorImpl,
  };
});

const TEST_RESOURCES = { en: { agents: enAgents, evolution: enEvolution } };

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

function renderCard(ui: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nWrapper>{ui}</I18nWrapper>
    </QueryClientProvider>,
  );
}

const governancePendingAck = {
  grant: {
    grant_id: "grant-1",
    workspace_id: "ws-1",
    tenant_status: "pending_owner_ack",
    tenant_policy_version: 4,
    tenant_granted_by: "user:owner",
    tenant_granted_at: "2026-08-20T00:00:00Z",
    pooled_status: "disabled",
    pooled_policy_version: 0,
    pooled_granted_by: "",
    pooled_granted_at: "",
  },
  policy: {
    selection_enabled: false,
    execution_enabled: false,
    reward_policy_version: 2,
    per_agent_sample_cap: 200,
    per_channel_sample_cap: 1000,
    per_workspace_sample_cap: 5000,
  },
};

const governanceActive = {
  grant: { ...governancePendingAck.grant, tenant_status: "active" },
  policy: governancePendingAck.policy,
};

const retentionBootstrap = {
  policy: { version: 1, trajectory_hot_days: 90, archive_days: 365, trace_hot_days: 30 },
  caps: { trajectory_hot_days: 90, archive_days: 365, trace_hot_days: 30 },
};

const ledgerWithFailures = {
  recalls_by_status: { hit: 7, miss: 3 },
  recalls_by_error_kind: { provider_timeout: 2 },
  trajectories_by_status: { terminal: 5 },
  trajectories_by_dive_status: { graded: 4 },
  avg_rounds: 2.5,
  p95_rounds: 4,
  graded_trajectories: 4,
  overall_reward_min: -1,
  overall_reward_avg: 0.5,
  dive_jobs_by_status: { succeeded: 3, failed: 1 },
  dive_job_attempts: 4,
  last_failure: { kind: "dive_model", message: "provider 500" },
  reward_outbox_by_status: { delivered: 5, failed: 1 },
  oldest_pending_age_seconds: 61.5,
  offline_export_eligible: 2,
  catalog_items: 3,
};

const cleanLedger = {
  recalls_by_status: { hit: 4 },
  recalls_by_error_kind: {},
  trajectories_by_status: { terminal: 4 },
  trajectories_by_dive_status: { graded: 4 },
  avg_rounds: 2,
  p95_rounds: 3,
  graded_trajectories: 4,
  overall_reward_min: 0,
  overall_reward_avg: 0.4,
  dive_jobs_by_status: { succeeded: 4 },
  dive_job_attempts: 4,
  last_failure: { kind: "", message: "" },
  reward_outbox_by_status: { delivered: 4 },
  oldest_pending_age_seconds: 0,
  offline_export_eligible: 1,
  catalog_items: 2,
};

describe("LegacyCurationNotApplicableCard", () => {
  it("states that legacy curation is not applicable in graph mode", () => {
    render(<LegacyCurationNotApplicableCard />, { wrapper: I18nWrapper });
    expect(screen.getByText(enEvolution.legacyCurationNotApplicable)).toBeTruthy();
    expect(screen.getByText(enEvolution.legacyCurationNotApplicableHint)).toBeTruthy();
  });
});


describe("GraphMemoryStatusCard", () => {
  it("renders the federated research graph row with its node count", () => {
    const workspaceId = "ws-1";
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    queryClient.setQueryData(evolutionKeys.graphMemoryStatus(workspaceId), {
      workspace_id: workspaceId,
      memory_type: "graph" as const,
      scoped_writer_ready: true,
      empty_start: false,
      graphs: [
        {
          kind: "research" as const,
          owner_id: workspaceId,
          current_version: 3,
          versions: [1, 2, 3],
          staging_segments: 0,
          last_consolidated_at: null,
          consolidation_backoff: false,
          recall_queries_24h: 4,
          recall_hit_rate_24h: 0.75,
          node_count: 12,
        },
      ],
    });

    render(
      <QueryClientProvider client={queryClient}>
        <I18nWrapper>
          <GraphMemoryStatusCard wsId={workspaceId} />
        </I18nWrapper>
      </QueryClientProvider>,
    );

    expect(screen.getByText("research")).toBeTruthy();
    expect(screen.getByText(workspaceId)).toBeTruthy();
    // Research governance stats: staging stays 0 and the node count surfaces.
    expect(screen.getByText(`${enEvolution.graphStaging}: 0`)).toBeTruthy();
    expect(screen.getByText(`${enEvolution.graphNodes}: 12`)).toBeTruthy();
  });

  it("renders no graph rows for a legacy workspace", () => {
    const workspaceId = "ws-legacy";
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    queryClient.setQueryData(evolutionKeys.graphMemoryStatus(workspaceId), {
      workspace_id: workspaceId,
      memory_type: "legacy" as const,
      scoped_writer_ready: false,
      empty_start: true,
      graphs: [],
    });

    render(
      <QueryClientProvider client={queryClient}>
        <I18nWrapper>
          <GraphMemoryStatusCard wsId={workspaceId} />
        </I18nWrapper>
      </QueryClientProvider>,
    );

    expect(screen.queryByText("research")).toBeNull();
    expect(screen.queryByText("project")).toBeNull();
  });
});


describe("GraphMemoryAgentModeCard", () => {
  it("reuses the interactive Computer to Runtime to Model selection chain", () => {
    const workspaceId = "ws-1";
    const runtimeId = "runtime-pi";
    const runtime = {
      id: runtimeId,
      workspace_id: workspaceId,
      daemon_id: "daemon-1",
      name: "Developer computer",
      runtime_mode: "local",
      provider: "pi",
      status: "online",
      device_info: "Linux",
      metadata: {},
      current_version: null,
      update_state: "idle",
      runtime_health: "healthy",
      owner_id: "user-1",
      visibility: "private",
      last_seen_at: new Date().toISOString(),
      computer_connected: true,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    } as unknown as RuntimeDevice;
    const profile = {
      ...EMPTY_GRAPH_MEMORY_PROFILE,
      workspace_id: workspaceId,
      memory_type: "graph" as const,
      graph_memory_mode: "agent" as const,
      memory_agent_runtime_id: runtimeId,
      memory_agent_model: "provider/model-1",
      config_version: 1,
      updated_at: new Date().toISOString(),
    };
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    queryClient.setQueryData(evolutionKeys.graphMemoryProfile(workspaceId), profile);
    queryClient.setQueryData(["runtimes", workspaceId, "computers"], [
      {
        daemon_id: "daemon-1",
        owner_id: "user-1",
        connected: true,
        last_seen_at: new Date().toISOString(),
        runtimes: [{ id: runtimeId, provider: "pi" }],
      },
    ]);
    queryClient.setQueryData(runtimeModelsKeys.forRuntime(runtimeId), {
      supported: true,
      customModelIdSupported: false,
      thinkingDiscovery: false,
      models: [{ id: "provider/model-1", label: "Model 1", provider: "provider" }],
    });

    render(
      <QueryClientProvider client={queryClient}>
        <I18nWrapper>
          <GraphMemoryAgentModeCard
            wsId={workspaceId}
            isAdmin
            runtimes={[runtime]}
            members={[]}
            currentUserId="user-1"
          />
        </I18nWrapper>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("runtime-config-fields")).toBeTruthy();
    expect(screen.getByTestId("computer-picker-trigger")).toBeEnabled();
    expect(screen.getByTestId("runtime-picker-trigger")).toBeEnabled();
    expect(screen.getByTestId("model-dropdown-trigger")).toBeEnabled();
    expect(document.querySelector("#graph-memory-agent-runtime")).toBeNull();
    expect(document.querySelector("#graph-memory-agent-model")).toBeNull();
  });
});

describe("TrainingGovernanceCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("surfaces pending owner acknowledgement and acks it with the seen CAS version", async () => {
    vi.mocked(api.getTrainingGovernance).mockResolvedValue(governancePendingAck);
    vi.mocked(api.updateTrainingGrant).mockResolvedValue({
      grant: { ...governancePendingAck.grant, tenant_status: "active" },
      policy: governancePendingAck.policy,
    });

    renderCard(<TrainingGovernanceCard wsId="ws-1" isAdmin />);

    expect(await screen.findByText(enEvolution.graphTrainingStatusPendingOwnerAck)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: enEvolution.graphTrainingAck }));
    await waitFor(() =>
      expect(api.updateTrainingGrant).toHaveBeenCalledWith("ws-1", {
        purpose: "tenant",
        action: "ack",
        expected_version: 4,
      }),
    );
  });

  it("toggles selection through the policy endpoint and recovers from a 409 conflict", async () => {
    vi.mocked(api.getTrainingGovernance).mockResolvedValue(governanceActive);
    vi.mocked(api.updateTrainingPolicy).mockRejectedValueOnce(new ApiError("training grant version conflict", 409, "Conflict", {}));

    renderCard(<TrainingGovernanceCard wsId="ws-1" isAdmin />);

    fireEvent.click(await screen.findByRole("switch", { name: enEvolution.graphTrainingSelection }));
    await waitFor(() => expect(api.updateTrainingPolicy).toHaveBeenCalledWith("ws-1", { selection_enabled: true }));
    expect(await screen.findByText(enEvolution.graphTrainingConflict)).toBeTruthy();
    await waitFor(() => expect(api.getTrainingGovernance).toHaveBeenCalledTimes(2));
  });

  it("revokes the tenant grant with the current version", async () => {
    vi.mocked(api.getTrainingGovernance).mockResolvedValue(governanceActive);
    vi.mocked(api.updateTrainingGrant).mockResolvedValue({
      workspace_id: "ws-1",
      purpose: "tenant",
      invalidated: 2,
      revoked_samples: 12,
      deletion_ledger_rows: 5,
    });

    renderCard(<TrainingGovernanceCard wsId="ws-1" isAdmin />);

    fireEvent.click(await screen.findByRole("button", { name: enEvolution.graphTrainingRevoke }));
    await waitFor(() =>
      expect(api.updateTrainingGrant).toHaveBeenCalledWith("ws-1", {
        purpose: "tenant",
        action: "revoke",
        expected_version: 4,
      }),
    );
  });

  it("hides owner-only surfaces from regular members", () => {
    vi.mocked(api.getTrainingGovernance).mockResolvedValue(governancePendingAck);
    const { container } = renderCard(<TrainingGovernanceCard wsId="ws-1" isAdmin={false} />);
    expect(container.firstChild).toBeNull();
    expect(api.getTrainingGovernance).not.toHaveBeenCalled();
  });
});

describe("RetentionCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("bootstraps with the 90/365/30 defaults and shows the platform caps", async () => {
    vi.mocked(api.getMemoryRetention).mockResolvedValue(retentionBootstrap);

    renderCard(<RetentionCard wsId="ws-1" isAdmin />);

    expect(await screen.findByDisplayValue("90")).toBeTruthy();
    expect(screen.getByDisplayValue("365")).toBeTruthy();
    expect(screen.getByDisplayValue("30")).toBeTruthy();
    expect(screen.getByText(`${enEvolution.graphRetentionCaps}: 90 / 365 / 30`)).toBeTruthy();
    expect(screen.getByText(`${enEvolution.graphRetentionVersion}: 1`)).toBeTruthy();
  });

  it("saves a shortened window with the seen policy version", async () => {
    vi.mocked(api.getMemoryRetention).mockResolvedValue(retentionBootstrap);
    vi.mocked(api.updateMemoryRetention).mockResolvedValue({
      policy: { version: 2, trajectory_hot_days: 90, archive_days: 180, trace_hot_days: 30 },
      caps: retentionBootstrap.caps,
    });

    renderCard(<RetentionCard wsId="ws-1" isAdmin />);
    fireEvent.change(await screen.findByLabelText(enEvolution.graphRetentionArchive), { target: { value: "180" } });
    fireEvent.click(screen.getByRole("button", { name: enEvolution.graphRetentionSave }));

    await waitFor(() =>
      expect(api.updateMemoryRetention).toHaveBeenCalledWith("ws-1", {
        trajectory_hot_days: 90,
        archive_days: 180,
        trace_hot_days: 30,
        expected_version: 1,
      }),
    );
  });

  it("maps over-cap edits to the platform-cap message", async () => {
    vi.mocked(api.getMemoryRetention).mockResolvedValue(retentionBootstrap);
    vi.mocked(api.updateMemoryRetention).mockRejectedValueOnce(new ApiError("retention policy exceeds platform caps", 422, "Unprocessable Entity", {}));

    renderCard(<RetentionCard wsId="ws-1" isAdmin />);
    fireEvent.change(await screen.findByLabelText(enEvolution.graphRetentionArchive), { target: { value: "400" } });
    fireEvent.click(screen.getByRole("button", { name: enEvolution.graphRetentionSave }));

    expect(await screen.findByText(enEvolution.graphRetentionCapError)).toBeTruthy();
  });

  it("maps a stale version to the conflict message and refetches", async () => {
    vi.mocked(api.getMemoryRetention).mockResolvedValue(retentionBootstrap);
    vi.mocked(api.updateMemoryRetention).mockRejectedValueOnce(new ApiError("retention policy version conflict", 409, "Conflict", {}));

    renderCard(<RetentionCard wsId="ws-1" isAdmin />);
    fireEvent.change(await screen.findByLabelText(enEvolution.graphRetentionArchive), { target: { value: "180" } });
    fireEvent.click(screen.getByRole("button", { name: enEvolution.graphRetentionSave }));

    expect(await screen.findByText(enEvolution.graphRetentionConflict)).toBeTruthy();
    await waitFor(() => expect(api.getMemoryRetention).toHaveBeenCalledTimes(2));
  });
});

describe("MemoryHealthCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("summarizes backlog, backoff and ledger failures as pipeline health", async () => {
    vi.mocked(api.getGraphMemoryStatus).mockResolvedValue({
      workspace_id: "ws-1",
      memory_type: "graph",
      scoped_writer_ready: true,
      empty_start: false,
      graphs: [{
        kind: "channel",
        owner_id: "c-1",
        node_count: 12,
        current_version: 3,
        versions: [1, 2, 3],
        staging_segments: 7,
        last_consolidated_at: null,
        consolidation_backoff: true,
        recall_queries_24h: 5,
        recall_hit_rate_24h: 0.5,
      }],
    });
    vi.mocked(api.getGraphMemoryAudit).mockResolvedValue({
      workspace_id: "ws-1",
      queries_24h: 5,
      recall_hits_24h: 2,
      recall_hit_rate_24h: 0.4,
      avg_explore_rounds_24h: 2,
      judged_queries_24h: 4,
      regressions_total: 0,
      ledger: ledgerWithFailures,
    });

    renderCard(<MemoryHealthCard wsId="ws-1" />);

    expect(await screen.findByText(enEvolution.graphHealthBackoff)).toBeTruthy();
    expect(screen.getByText(`${enEvolution.graphHealthStaging}: 7`)).toBeTruthy();
    expect(screen.getByText("provider_timeout: 2")).toBeTruthy();
    expect(screen.getByText(`${enEvolution.graphHealthOutboxFailed}: 1`)).toBeTruthy();
    expect(screen.getByText(`${enEvolution.graphHealthDiveFailed}: 1`)).toBeTruthy();
  });

  it("reports a clean ledger when nothing failed", async () => {
    vi.mocked(api.getGraphMemoryStatus).mockResolvedValue({
      workspace_id: "ws-1",
      memory_type: "graph",
      scoped_writer_ready: true,
      empty_start: false,
      graphs: [{
        kind: "project",
        owner_id: "p-1",
        node_count: 8,
        current_version: 2,
        versions: [1, 2],
        staging_segments: 0,
        last_consolidated_at: "2026-08-24T00:00:00Z",
        consolidation_backoff: false,
        recall_queries_24h: 4,
        recall_hit_rate_24h: 1,
      }],
    });
    vi.mocked(api.getGraphMemoryAudit).mockResolvedValue({
      workspace_id: "ws-1",
      queries_24h: 4,
      recall_hits_24h: 4,
      recall_hit_rate_24h: 1,
      avg_explore_rounds_24h: 1.5,
      judged_queries_24h: 4,
      regressions_total: 0,
      ledger: cleanLedger,
    });

    renderCard(<MemoryHealthCard wsId="ws-1" />);

    expect(await screen.findByText(enEvolution.graphHealthClean)).toBeTruthy();
  });
});
