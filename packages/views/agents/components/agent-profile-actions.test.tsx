// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, AgentRuntime, MemberWithUser } from "@multica/core/types";
import { AgentProfileActions } from "./agent-profile-actions";

const mocks = vi.hoisted(() => ({
  openDM: vi.fn(),
  isPending: false,
  copyText: vi.fn(async (..._args: unknown[]) => true),
  openModal: vi.fn(),
  setDraft: vi.fn(),
  cancelAgentTasks: vi.fn(async (..._args: unknown[]) => ({ cancelled: 1 })),
  archiveAgent: vi.fn(async (..._args: unknown[]) => ({})),
  invalidateQueries: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => ({ openDM: mocks.openDM, isPending: mocks.isPending }),
}));

vi.mock("@multica/ui/lib/clipboard", () => ({
  copyText: (...args: unknown[]) => mocks.copyText(...args),
}));

vi.mock("@multica/core/modals", () => ({
  useModalStore: {
    getState: () => ({ open: mocks.openModal }),
  },
}));

vi.mock("@multica/core/issues/stores/draft-store", () => ({
  useIssueDraftStore: {
    getState: () => ({ setDraft: mocks.setDraft }),
  },
}));

vi.mock("@multica/core/api", () => ({
  api: {
    cancelAgentTasks: (...args: unknown[]) => mocks.cancelAgentTasks(...args),
    archiveAgent: (...args: unknown[]) => mocks.archiveAgent(...args),
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
}));

vi.mock("sonner", () => ({
  toast: {
    success: (...args: unknown[]) => mocks.toastSuccess(...args),
    error: (...args: unknown[]) => mocks.toastError(...args),
  },
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: typeof RESOURCES) => string, _vars?: Record<string, unknown>) =>
      selector(RESOURCES),
  }),
}));

const RESOURCES = {
  side_panel: {
    actions_section: "Actions",
    message_button: "Message",
    message_opening: "Opening…",
    actions_restart: "Restart / Reset",
    actions_restart_dialog_title: "Restart?",
    actions_restart_dialog_description: "Cancels tasks.",
    actions_copy_diagnostic: "Copy diagnostic info",
    actions_copy_success: "Copied",
    actions_copy_failed: "Copy failed",
    actions_report: "Report issue",
    actions_report_title: "Issue with agent Atlas",
    actions_report_body_intro: "Please describe",
    actions_archive: "Archive agent",
  },
  row_actions: {
    no_tasks_to_cancel_toast: "No tasks",
    cancelled_tasks_toast: "Cancelled",
    cancel_failed_toast: "Cancel failed",
    agent_archived_toast: "Archived",
    archive_failed_toast: "Archive failed",
    cancel_dialog_keep: "Keep",
  },
  detail: {
    archive_dialog_title: "Archive?",
    archive_dialog_description: "Archive Atlas",
    archive_dialog_cancel: "Cancel",
    archive_dialog_confirm: "Archive",
  },
};

const agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "runtime-1",
  name: "atlas",
  display_name: "Atlas",
  description: "desc",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
  status: "idle",
  max_concurrent_tasks: 1,
  model: "auto",
  thinking_level: "",
  managed_role: undefined,
  owner_id: "user-owner",
  skills: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  archived_at: null,
  archived_by: null,
} as Agent;

const runtime = { id: "runtime-1", name: "Cursor" } as AgentRuntime;
const members = [
  {
    id: "m1",
    user_id: "user-owner",
    workspace_id: "ws-1",
    role: "member",
    name: "Owner",
    display_name: "Owner",
    email: "o@x.com",
    avatar_url: null,
    profile_description: "",
    created_at: "2026-01-01T00:00:00Z",
  },
] as MemberWithUser[];

describe("AgentProfileActions (LRM-448)", () => {
  beforeEach(() => {
    mocks.openDM.mockReset();
    mocks.copyText.mockReset().mockResolvedValue(true);
    mocks.openModal.mockReset();
    mocks.setDraft.mockReset();
    mocks.cancelAgentTasks.mockReset().mockResolvedValue({ cancelled: 1 });
    mocks.archiveAgent.mockReset().mockResolvedValue({});
    mocks.isPending = false;
  });

  it("renders Message as primary action and opens DM", () => {
    render(
      <AgentProfileActions agent={agent} runtime={runtime} members={members} canManage />,
    );
    fireEvent.click(screen.getByTestId("agent-profile-action-message"));
    expect(mocks.openDM).toHaveBeenCalledWith({ peer_type: "agent", peer_id: "agent-1" });
  });

  it("copies diagnostic info without silent failure (LRM-238)", async () => {
    render(
      <AgentProfileActions agent={agent} runtime={runtime} members={members} canManage />,
    );
    fireEvent.click(screen.getByTestId("agent-profile-action-copy"));
    expect(mocks.copyText).toHaveBeenCalled();
    await vi.waitFor(() => {
      expect(mocks.toastSuccess).toHaveBeenCalledWith("Copied");
    });
  });

  it("opens create-issue with diagnostic draft on Report", () => {
    render(
      <AgentProfileActions agent={agent} runtime={runtime} members={members} canManage />,
    );
    fireEvent.click(screen.getByTestId("agent-profile-action-report"));
    expect(mocks.setDraft).toHaveBeenCalled();
    expect(mocks.openModal).toHaveBeenCalledWith("create-issue");
  });

  it("hides manage-only actions when canManage is false", () => {
    render(
      <AgentProfileActions
        agent={agent}
        runtime={runtime}
        members={members}
        canManage={false}
      />,
    );
    expect(screen.queryByTestId("agent-profile-action-reset")).not.toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-archive")).not.toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-message")).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-copy")).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-report")).toBeInTheDocument();
  });

  it("isolates Archive in a danger bottom zone for managers", () => {
    render(
      <AgentProfileActions agent={agent} runtime={runtime} members={members} canManage />,
    );
    const archive = screen.getByTestId("agent-profile-action-archive");
    expect(archive.className).toMatch(/text-destructive/);
    expect(archive.parentElement?.className).toMatch(/border-t/);
  });
});
